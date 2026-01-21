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
	"strings"
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

	// Synesthetic state
	bgColor       color.RGBA
	targetBgColor color.RGBA

	// Conversation State
	lastWordTime   time.Time
	currentSpeaker int // 0: Left, 1: Right
	silenceStage   int // 0: None, 1: Dots, 2: Ma, 3: Chinmoku
}

type BarrageWord struct {
	Text     string
	X, Y     float64
	VX, VY   float64
	Scale    float64
	Color    color.Color // Changed from color.RGBA to color.Color to match typical Ebiten
	Life     int
	MaxLife  int
	IsGlitch bool

	// Physics
	Rotation  float64
	VRotation float64
	IsResting bool
	IsFiller  bool
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

	// Update Barrage with Physics
	newBarrage := []BarrageWord{}
	gravity := 0.25
	floorY := float64(ScreenHeight) - 100.0

	for _, b := range g.barrage {
		// Apply Physics if not resting
		if !b.IsResting {
			// Gravity (lighter for fillers)
			grav := gravity
			if b.IsFiller {
				grav *= 0.2
			}

			b.VY += grav
			b.X += b.VX
			b.Y += b.VY
			b.Rotation += b.VRotation

			// Friction
			b.VX *= 0.98
			b.VRotation *= 0.98

			// Floor Collision
			if b.Y > floorY {
				b.Y = floorY
				b.VY *= -0.6 // Bounce
				b.VX *= 0.8  // Floor friction

				// Stop if slow enough
				if math.Abs(b.VY) < 1.0 {
					b.IsResting = true
					b.VY = 0
				}
			}

			// Wall Bounce
			if b.X < 50 || b.X > ScreenWidth-50 {
				b.VX *= -0.8
				b.X += b.VX // Push back
			}
		}

		b.Life--
		if b.Life > 0 {
			newBarrage = append(newBarrage, b)
		}
	}
	g.barrage = newBarrage

	// Color Interpolation (Lerp)
	// If SPLIT, force Red
	if g.state.CurrentState == "SPLIT" {
		g.targetBgColor = ColRed
	}

	g.bgColor = lerpColor(g.bgColor, g.targetBgColor, 0.05)

	// Check Silence Duration
	silenceDur := time.Since(g.lastWordTime)

	if silenceDur > 2*time.Second && g.silenceStage == 0 {
		g.spawnWordInternal("...", false)
		g.silenceStage = 1
	}
	if silenceDur > 5*time.Second && g.silenceStage == 1 {
		g.spawnWordInternal("間", false)
		g.silenceStage = 2
	}
	if silenceDur > 8*time.Second && g.silenceStage == 2 {
		g.spawnWordInternal("沈黙", false) // Heavy fall
		g.silenceStage = 3
	}
	if silenceDur > 12*time.Second && g.silenceStage == 3 {
		g.spawnWordInternal("静寂", false) // Another heavy fall
		g.silenceStage = 4
	}

	g.mu.Unlock()

	return nil
}

func lerpColor(c1, c2 color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c1.R) + float64(int(c2.R)-int(c1.R))*t),
		G: uint8(float64(c1.G) + float64(int(c2.G)-int(c1.G))*t),
		B: uint8(float64(c1.B) + float64(int(c2.B)-int(c1.B))*t),
		A: 255,
	}
}

func textToColor(text string) color.RGBA {
	hash := 0
	for _, c := range text {
		hash = int(c) + ((hash << 5) - hash)
	}

	// Generate HSV-ish (High Saturation/Value for Shaft look)
	h := math.Abs(float64(hash % 360))
	s := 0.8
	v := 0.2 // Dark background usually, but let's try 0.2 for colored darks

	// Quick HSV to RGB
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
	m := v - c

	var r, g, b float64
	switch {
	case 0 <= h && h < 60:
		r, g, b = c, x, 0
	case 60 <= h && h < 120:
		r, g, b = x, c, 0
	case 120 <= h && h < 180:
		r, g, b = 0, c, x
	case 180 <= h && h < 240:
		r, g, b = 0, x, c
	case 240 <= h && h < 300:
		r, g, b = x, 0, c
	case 300 <= h && h < 360:
		r, g, b = c, 0, x
	}

	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

// Public wrapper with Lock (safe for external calls)
func (g *Game) spawnWord(text string, glitch bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnWordInternal(text, glitch)
}

// Internal implementation (Assumes Lock is held)
func (g *Game) spawnWordInternal(text string, glitch bool) {
	// Reset Silence Stage on legitimate input (not generated silence words)
	isSilenceWord := (text == "..." || text == "間" || text == "沈黙" || text == "静寂")
	if !isSilenceWord {
		g.silenceStage = 0

		// Update Target Background based on text
		if !glitch && g.state.CurrentState != "SPLIT" {
			g.targetBgColor = textToColor(text)
		}
	}

	// Conversation Turn Logic (Only for real words)
	if !isSilenceWord {
		now := time.Now()
		silenceDuration := now.Sub(g.lastWordTime)
		g.lastWordTime = now

		// If silence > 2.0s (was 1.5), switch speaker
		if silenceDuration > 2000*time.Millisecond {
			g.currentSpeaker = (g.currentSpeaker + 1) % 2
		}
	}

	// Physics Init
	// Nuance: Volume -> Scale
	// We need instantaneous volume here. g.micVolume is smoothed.
	// Let's use g.micVolume as a proxy for now.
	nuanceScale := 1.0 + (g.micVolume * 3.0) // 1.0 to 4.0
	if nuanceScale > 4.0 {
		nuanceScale = 4.0
	}

	scale := nuanceScale + rand.Float64()*0.5
	if glitch {
		scale *= 1.5
	}

	// Custom Physics for Silence Words
	life := 600
	colorVal := ColWhite
	var startX, startY, vx, vy float64

	if text == "..." {
		scale = 1.0
		life = 300
		startX = rand.Float64() * float64(ScreenWidth)
		startY = rand.Float64() * float64(ScreenHeight)
		vx = (rand.Float64() - 0.5) * 0.5
		vy = (rand.Float64() - 0.5) * 0.5         // Float gently
		colorVal = color.RGBA{100, 100, 100, 100} // Grey transparent
	} else if text == "間" {
		scale = 3.0
		life = 800
		startX = float64(ScreenWidth) / 2
		startY = float64(ScreenHeight) / 3
		vx = 0
		vy = 0.05                                 // Very slow sink
		colorVal = color.RGBA{200, 200, 255, 200} // Bluish White
	} else if text == "沈黙" || text == "静寂" {
		scale = 5.0 // Massive
		life = 1000
		startX = float64(ScreenWidth) / 2
		startY = 0 // Drop from top
		vx = 0
		vy = 15.0                              // Heavy drop
		colorVal = color.RGBA{50, 50, 50, 255} // Dark Grey (Almost Black)
	} else {
		// Normal Word Physics
		// Position based on Speaker
		if g.state.CurrentState == "SPLIT" {
			// Chaos: Center Eruption
			startX = float64(ScreenWidth/2) + rand.Float64()*400 - 200
			vx = (rand.Float64() - 0.5) * 10
		} else if g.currentSpeaker == 0 {
			// Left Speaker (throws to right)
			startX = float64(ScreenWidth)*0.2 + rand.Float64()*100
			vx = 5.0 + rand.Float64()*5.0
		} else {
			// Right Speaker (throws to left)
			startX = float64(ScreenWidth)*0.8 - rand.Float64()*100
			vx = -5.0 - rand.Float64()*5.0
		}
		startY = float64(ScreenHeight)*0.4 + rand.Float64()*200 - 100
		vy = -5.0 - rand.Float64()*5.0
	}

	bw := BarrageWord{
		Text:      text,
		X:         startX,
		Y:         startY,
		VX:        vx,
		VY:        vy,
		Scale:     scale,
		Color:     colorVal, // Default white, draw calc handles glitch color
		Life:      life,     // Longer life for piling up
		MaxLife:   life,
		IsGlitch:  glitch,
		Rotation:  (rand.Float64() - 0.5) * 0.5,
		VRotation: (rand.Float64() - 0.5) * 0.1,
		IsResting: false,
		IsFiller:  len(text) <= 3 && !isSilenceWord, // Simple check for fillers
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
	// SPLIT -> Red Background (Handled in Update via targetBgColor override)
	// ALIGNED -> Dynamic Synesthetic Color (Handled in Update)

	// screen.Fill(g.bgColor) // Direct fill

	// Let's use a "Vignette" or simple fill for now
	screen.Fill(g.bgColor)

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
		audioChan:     make(chan float64, 10),
		bgColor:       ColBlack,
		targetBgColor: ColBlack,
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

					// 1. Send to Ruby (If connected)
					if game.conn != nil {
						msg := map[string]string{
							"type": "speech_text",
							"text": txt,
						}
						game.conn.WriteJSON(msg)
					}

					// 2. Local Atmosphere Logic (Fallback)
					dangerWords := []string{"嘘", "矛盾", "違う", "だめ", "無理", "変", "おかしい", "バグ", "ミス", "否定", "NO", "嫌", "怖い"}
					isDanger := false
					for _, dw := range dangerWords {
						if strings.Contains(txt, dw) {
							isDanger = true
							break
						}
					}

					isGlitch := len(txt) < 3

					if isDanger {
						game.mu.Lock()
						game.state.CurrentState = "SPLIT"
						game.mu.Unlock()
						// Reset after 3 seconds?
						// ideally use a timer in Update(), but for now just let it stick or decay.
						// Let's launch a decay timer goroutine
						go func() {
							time.Sleep(2 * time.Second)
							game.mu.Lock()
							game.state.CurrentState = "UNKNOWN" // Revert to neutral
							game.mu.Unlock()
						}()
						isGlitch = true // Danger words always glitch
					} else {
						// Green/White interaction usually implies alignment, but we default to UNKNOWN
						// If user says positive things?
						// For now, default is just spawning words.
					}

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
