package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/url"

	"github.com/gen2brain/malgo"
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
	ServerHost   = "localhost:4567"
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
	conn      *websocket.Conn
	state     State
	jpFace    font.Face
	jpFaceBig font.Face

	// Audio
	speech     *SpeechEngine
	audioChan  chan float64 
	
	micVolume  float64 // 0.0 - 1.0 (Smoothed)
	peakVolume float64
	
	// Visuals
	frameCount int
	videoGlitch float64 // For Shaft cut effect
	words      []string 
	flashWord  string
	flashTTL   int
}

// Audio Callback
func onRecvFrames(pOutputSample, pInputSample []byte, framecount uint32) {
	// Calculate RMS (roughly)
	// Input is assumed S16 (2 bytes per sample)
	// Very naive implementation for visualization
	sum := 0.0
	count := int(framecount) * 2 // Stereo? No, assuming mono/stereo depending on config but taking all bytes
	// Safe casting for S16 LE
	for i := 0; i < len(pInputSample); i += 2 {
		if i+1 >= len(pInputSample) {
			break
		}
		v16 := int16(uint16(pInputSample[i]) | uint16(pInputSample[i+1])<<8)
		val := float64(v16) / 32768.0
		sum += val * val
	}
	rms := math.Sqrt(sum / float64(framecount))
	// Pass to channel or global? For simplicity in this demo, accessing atomic/global might be needed,
	// but ebiten runs on main thread. We use channel.
	// This function is currently not used as audio is handled by SpeechEngine.
	// If it were used, it would need access to the game's audioChan.
	// audioChan <- rms
}


func (g *Game) Update() error {
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

	// Flash TTL
	if g.flashTTL > 0 {
		g.flashTTL--
		if g.flashTTL == 0 {
			g.flashWord = ""
		}
	}

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Shaft Aesthetic: Sharp Cuts based on state
	// Background
	bgColor := ColBlack
	if g.state.CurrentState == "SPLIT" && g.frameCount%10 < 5 {
		// Strobe effect on critical split
		bgColor = color.RGBA{30, 0, 0, 255}
	}
	screen.Fill(bgColor)

	// 1. Dynamic Background Geometry (Reacts to Audio)
	g.drawGeometry(screen)

	// 2. Typography (The "Conversation")
	g.drawTypography(screen)

	// Debug info
	ebitenutil.DebugPrint(screen, fmt.Sprintf("Vol: %.2f | State: %s", g.micVolume, g.state.CurrentState))
}

func (g *Game) drawGeometry(screen *ebiten.Image) {
	cx, cy := float32(ScreenWidth/2), float32(ScreenHeight/2)

	// Audio Reactive Circle/Line
	radius := float32(200.0 + g.micVolume*400.0)
	thickness := float32(2.0 + g.micVolume*10.0)

	// Rotate based on time
	theta := float64(g.frameCount) * 0.02

	// Draw Split Line (Shaft style is often sharp angles)
	x1 := cx + float32(math.Cos(theta))*radius
	y1 := cy + float32(math.Sin(theta))*radius
	x2 := cx - float32(math.Cos(theta))*radius
	y2 := cy - float32(math.Sin(theta))*radius

	col := ColWhite
	if g.state.CurrentState == "SPLIT" {
		col = ColRed
		// Double Line
		vector.StrokeLine(screen, x1+20, y1, x2+20, y2, thickness, col, true)
	}
	vector.StrokeLine(screen, x1, y1, x2, y2, thickness, col, true)
}

func (g *Game) drawTypography(screen *ebiten.Image) {
	// "Word Flash" - The core feature
	// If mic volume spikes, show random decorations or the "Flash Word"

	// Priority: Flash Word from Ruby > Audio Reactive Noise

	var textToDraw string
	var sizeScale float64 = 1.0
	var clr color.Color = ColWhite

	if g.flashWord != "" {
		textToDraw = g.flashWord
		sizeScale = 2.0
		clr = ColRed
	} else if g.micVolume > 0.1 && g.frameCount%20 == 0 {
		// Simulated conversational noise (if we had STT, this would be real words)
		// For now, use abstract symbols or Kanat
		opts := []string{"認識", "齟齬", "継続", "停止", "？", "..."}
		textToDraw = opts[rand.Intn(len(opts))]
		sizeScale = 0.5 + g.micVolume
		clr = ColYellow
	}

	if textToDraw != "" && g.jpFaceBig != nil {
		bounds := text.BoundString(g.jpFaceBig, textToDraw)
		w, h := bounds.Max.X-bounds.Min.X, bounds.Max.Y-bounds.Min.Y

		// Randomize position slightly for "Unease"
		jitter := 0
		if g.state.CurrentState == "SPLIT" {
			jitter = rand.Intn(50) - 25
		}

		// Center
		x := (ScreenWidth-w)/2 + jitter
		y := (ScreenHeight+h)/2 + jitter

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(float64(x), float64(y))

		// Tilt (Shaft Head Tilt)
		op.GeoM.Rotate(0.1 * math.Sin(float64(g.frameCount)*0.05))

		text.Draw(screen, textToDraw, g.jpFaceBig, x, y, clr)
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return ScreenWidth, ScreenHeight
}

func (g *Game) connect() {
	u := url.URL{Scheme: "ws", Host: ServerHost, Path: "/cable"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return
	}
	g.conn = c

	go func() {
		defer c.Close()
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				return
			}
			g.handleMessage(message)
		}
	}()
}

func (g *Game) handleMessage(msg []byte) {
	var data map[string]interface{}
	json.Unmarshal(msg, &data)
	if data["type"] == "flash" {
		if word, ok := data["word"].(string); ok {
			g.flashWord = word
			g.flashTTL = 60
		}
	} else {
		json.Unmarshal(msg, &g.state)
	}
}

func loadFont(size float64) font.Face {
	// Try system fonts
	fontPaths := []string{
		"C:\\Windows\\Fonts\\meiryo.ttc",
		"C:\\Windows\\Fonts\\msgothic.ttc",
	}
	var fontData []byte
	var err error
	for _, path := range fontPaths {
		fontData, err = ioutil.ReadFile(path)
		if err == nil {
			break
		}
	}
	if len(fontData) == 0 {
		return nil
	}

	tt, _ := opentype.Parse(fontData)
	face, _ := opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	return face
}

// initAudio function removed as per instruction

func main() {
	game := &Game{
		audioChan: make(chan float64, 10), // Initialize game's audioChan
	}
	game.jpFace = loadFont(64)   // Standard Text
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
					// Received recognized text!
					// Force flash this word
					fmt.Printf("RECOGNIZED: %s\n", txt)
					
					// Trigger standard "Flash" logic
					// Since this is a game loop, we should guard access or just set it
					// This runs in background, Game.Draw/Update runs in main thread.
					// Simple mutex or just atomic assignment string is mostly safe in Go for visualization
					game.flashWord = txt
					game.flashTTL = 120 // 2 seconds display for recognized text
				}
			}
		}()
	} else {
		log.Println("Speech Engine failed to initialize (Missing model?)")
	}
	
	ebiten.SetWindowSize(ScreenWidth/2, ScreenHeight/2)
	ebiten.SetWindowTitle("OVERLAY")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(game); err != nil { log.Fatal(err) }
}
```
