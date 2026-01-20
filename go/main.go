package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"net/url"
	"os"
	"time"

	"sync"

	"github.com/gorilla/websocket"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Config
const (
	ScreenWidth  = 1920
	ScreenHeight = 1080
	ServerHost   = "127.0.0.1:4567"
)

// Colors (Shaft Style)
var (
	ColRed    = color.RGBA{198, 40, 40, 255}
	ColBlack  = color.RGBA{10, 10, 10, 255}
	ColWhite  = color.RGBA{240, 240, 240, 255}
	ColYellow = color.RGBA{253, 216, 53, 255}
)

type State struct {
	CurrentState string  `json:"state"`
	SplitDegree  float64 `json:"split_degree"` // 0.0 - 1.0
	Strength     float64 `json:"strength"`
	FlashWord    string  `json:"word,omitempty"`
}

type Game struct {
	mu        sync.RWMutex
	conn      *websocket.Conn
	state     State
	jpFace    font.Face
	jpFaceBig font.Face

	// Audio
	speech    *SpeechEngine
	audioChan chan float64

	micVolume  float64 // 0.0 - 1.0 (Smoothed)
	peakVolume float64

	// Visuals
	frameCount  int
	videoGlitch float64 // For Shaft cut effect
	words       []string
	// 	flashWord   string
	// 	flashTTL    int
	barrage []BarrageWord
}

type BarrageWord struct {
	Text     string
	X, Y     float64
	VX, VY   float64
	Scale    float64
	Color    color.Color
	Life     int
	MaxLife  int
	IsGlitch bool
}

func (g *Game) Update() error {
	g.mu.Lock()
	g.frameCount++

	// Init channel if nil (hacky lazy init or do in main)
	if g.audioChan == nil {
		g.audioChan = make(chan float64, 10)
	}

	// Consume Audio
	select {
	case vol := <-g.audioChan:
		// Smooth decay, instant attack
		target := vol * 8.0 // High Gain for visuals
		if target > g.micVolume {
			g.micVolume = target
		} else {
			g.micVolume *= 0.92
		}
	default:
		g.micVolume *= 0.95
	}

	// Update Barrage
	newBarrage := []BarrageWord{}
	for _, b := range g.barrage {
		b.X += b.VX
		b.Y += b.VY
		b.Life--
		if b.Life > 0 {
			newBarrage = append(newBarrage, b)
		}
	}
	g.barrage = newBarrage

	g.mu.Unlock()

	return nil
}

func (g *Game) spawnWord(text string, glitch bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	scale := 1.0 + rand.Float64()
	if glitch {
		scale *= 1.5
	}

	// Start from random side or center? Barrage usually flows right to left or random.
	// Let's do "Eruption" style from center-ish for conversation.

	bw := BarrageWord{
		Text:     text,
		X:        float64(ScreenWidth/2) + rand.Float64()*400 - 200,
		Y:        float64(ScreenHeight/2) + rand.Float64()*200 - 100,
		VX:       (rand.Float64() - 0.5) * 5,
		VY:       (rand.Float64() - 0.5) * 5,
		Scale:    scale,
		Color:    ColWhite,
		Life:     180 + rand.Intn(60), // 3-4 seconds
		MaxLife:  200,
		IsGlitch: glitch,
	}

	if g.state.CurrentState == "SPLIT" || glitch {
		bw.Color = ColRed
		bw.VX *= 2.0
		bw.VY *= 2.0
	}

	g.barrage = append(g.barrage, bw)
}

func (g *Game) Draw(screen *ebiten.Image) {
	g.mu.RLock()
	currentState := g.state.CurrentState
	vol := g.micVolume
	g.mu.RUnlock()

	// Shaft Style Background Logic
	// SPLIT -> Red Background, Black Text (High Alert)
	// ALIGNED -> White Background, Black Text (Clarity)
	// UNKNOWN -> Black Background, White Text (Mystery)

	bgColor := ColBlack
	if currentState == "SPLIT" {
		bgColor = ColRed
		// Strobe effect on critical split
	}
	screen.Fill(bgColor)

	// 1. Dynamic Background Geometry (Reacts to Audio)
	g.drawGeometry(screen)

	// 2. Typography (The "Conversation")
	g.drawBarrage(screen)

	// Debug info
	ebitenutil.DebugPrint(screen, fmt.Sprintf("Vol: %.2f | State: %s", vol, currentState))
}

func (g *Game) drawGeometry(screen *ebiten.Image) {
	cx, cy := float32(ScreenWidth/2), float32(ScreenHeight/2)

	g.mu.RLock()
	micVolume := g.micVolume
	currentState := g.state.CurrentState
	g.mu.RUnlock()

	// Audio Reactive Circle/Line
	radius := float32(200.0 + micVolume*400.0)
	thickness := float32(2.0 + micVolume*10.0)

	// Rotate based on time
	theta := float64(g.frameCount) * 0.02

	// Draw Split Line (Shaft style is often sharp angles)
	x1 := cx + float32(math.Cos(theta))*radius
	y1 := cy + float32(math.Sin(theta))*radius
	x2 := cx - float32(math.Cos(theta))*radius
	y2 := cy - float32(math.Sin(theta))*radius

	col := ColWhite
	if currentState == "SPLIT" {
		col = ColRed
		// Double Line
		vector.StrokeLine(screen, x1+20, y1, x2+20, y2, thickness, col, true)
	}
	vector.StrokeLine(screen, x1, y1, x2, y2, thickness, col, true)
}

func (g *Game) drawBarrage(screen *ebiten.Image) {
	g.mu.RLock()
	words := make([]BarrageWord, len(g.barrage))
	copy(words, g.barrage)
	// currentState := g.state.CurrentState
	g.mu.RUnlock()

	if g.jpFaceBig == nil {
		return
	}

	for _, b := range words {
		bounds := text.BoundString(g.jpFaceBig, b.Text)
		w, h := bounds.Max.X-bounds.Min.X, bounds.Max.Y-bounds.Min.Y

		// Glitch Effect: Random Jitter
		jx, jy := 0.0, 0.0
		if b.IsGlitch || g.state.CurrentState == "SPLIT" {
			jx = (rand.Float64() - 0.5) * 10
			jy = (rand.Float64() - 0.5) * 10
		}

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(float64(-w/2), float64(-h/2))
		op.GeoM.Scale(b.Scale, b.Scale)
		op.GeoM.Rotate(0.1 * math.Sin(float64(g.frameCount)*0.05))
		op.GeoM.Translate(b.X+jx, b.Y+jy)

		// Alpha decay
		alpha := float64(b.Life) / float64(b.MaxLife)
		if alpha < 0.2 {
			alpha = 0.2
		}

		// Ebiten doesn't support direct alpha on text easily without shaders or color M,
		// but we can use ColorScale in newer versions or just stick to solid for Shaft style.
		// Shaft style is solid, so keep it solid.

		text.Draw(screen, b.Text, g.jpFaceBig, int(b.X+jx), int(b.Y+jy), b.Color)
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return ScreenWidth, ScreenHeight
}

func (g *Game) connect() {
	go func() {
		for {
			u := url.URL{Scheme: "ws", Host: ServerHost, Path: "/cable"}
			log.Printf("Connecting to %s...", u.String())

			c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				log.Printf("Connection failed: %v. Retrying in 1s...", err)
				time.Sleep(1 * time.Second)
				continue
			}

			log.Println("Connected to Server!")
			g.conn = c

			// Listen loop
			for {
				_, message, err := c.ReadMessage()
				if err != nil {
					log.Println("Read error (Disconnected):", err)
					break // Break inner loop to reconnect
				}
				g.handleMessage(message)
			}

			g.conn.Close()
			g.conn = nil
			time.Sleep(1 * time.Second)
		}
	}()
}

func (g *Game) handleMessage(msg []byte) {
	var data map[string]interface{}
	if err := json.Unmarshal(msg, &data); err != nil {
		log.Printf("Error unmarshaling message: %v", err)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if msgType, ok := data["type"].(string); ok && msgType == "flash" {
		if word, ok := data["word"].(string); ok {
			// Manual trigger via Admin spawning a glitch word
			// We need to unlock to call spawnWord because spawnWord locks.
			// Ideally refactor spawnWord to internalSpawnWord without lock.
			// For now, let's just do logic inline since we are locked.

			// DUPLICATE LOGIC FROM spawnWord but inline to avoid deadlock
			scale := 2.0
			bw := BarrageWord{
				Text:     word,
				X:        float64(ScreenWidth/2) + rand.Float64()*100 - 50,
				Y:        float64(ScreenHeight/2) + rand.Float64()*100 - 50,
				VX:       (rand.Float64() - 0.5) * 10,
				VY:       (rand.Float64() - 0.5) * 10,
				Scale:    scale,
				Color:    ColRed,
				Life:     300,
				MaxLife:  300,
				IsGlitch: true,
			}
			g.barrage = append(g.barrage, bw)
		}
	} else {
		// Assume it's a full state update if not a "flash" message
		// This will overwrite the entire g.state struct
		var newState State
		if err := json.Unmarshal(msg, &newState); err != nil {
			log.Printf("Error unmarshaling state message: %v", err)
			return
		}
		g.state = newState
	}
}

func loadFont(size float64) font.Face {
	// Try local asset first (Reliable)
	fontPaths := []string{
		"assets/font.otf",
		"C:\\Windows\\Fonts\\meiryo.ttc",
	}

	var fontData []byte
	var err error
	var pathUsed string

	for _, path := range fontPaths {
		fontData, err = os.ReadFile(path)
		if err == nil {
			pathUsed = path
			break
		}
	}
	if len(fontData) == 0 {
		log.Println("WARNING: No font found. Falling back to nil (Will Crash if used)")
		return nil
	}

	// Parse
	tt, err := opentype.Parse(fontData)
	if err != nil {
		// If TTC, try collection
		if len(pathUsed) > 3 && pathUsed[len(pathUsed)-3:] == "ttc" {
			coll, err := opentype.ParseCollection(fontData)
			if err == nil && coll.NumFonts() > 0 {
				tt, _ = coll.Font(0) // Use first font
			}
		}
	}

	if tt == nil {
		log.Printf("Failed to parse font from %s: %v", pathUsed, err)
		return nil
	}

	face, err := opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		log.Printf("Failed to create face: %v", err)
		return nil
	}

	return face
}

// initAudio function removed as per instruction

func main() {
	game := &Game{
		audioChan: make(chan float64, 10), // Initialize game's audioChan
	}
	game.jpFace = loadFont(64)     // Standard Text
	game.jpFaceBig = loadFont(200) // Huge Impact Text
	game.connect()

	// Start Speech Engine
	se := NewSpeechEngine()
	if se != nil {
		game.speech = se
		go se.Start()

		// Pipe channels
		go func() {
			for {
				select {
				case vol := <-se.VolChan:
					game.audioChan <- vol
				case txt := <-se.TextChan:
					fmt.Printf("RECOGNIZED: %s\n", txt)

					// 1. Send to Ruby (The Brain)
					if game.conn != nil {
						msg := map[string]string{
							"type": "speech_text",
							"text": txt,
						}
						game.conn.WriteJSON(msg)
					}

					// 2. Trigger Visuals (The Body)
					// Glitch if confidence low (simulated by short words for now) or random
					isGlitch := len(txt) < 3 // Short words might be noise?
					game.spawnWord(txt, isGlitch)
				}
			}
		}()
	} else {
		log.Println("Speech Engine failed to initialize (Missing model?)")
	}

	ebiten.SetWindowSize(ScreenWidth/2, ScreenHeight/2)
	ebiten.SetWindowTitle("OVERLAY")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
