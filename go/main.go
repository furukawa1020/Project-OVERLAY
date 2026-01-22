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

	// Visual Cache
	Image  *ebiten.Image
	ScaleX float64 // For horizontal mirroring (-1.0)
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
	style := "normal"
	if glitch {
		style = "glitch"
	}
	g.spawnWordInternal(text, style)
}

// Internal implementation (Assumes Lock is held)
func (g *Game) spawnWordInternal(text string, glitch bool) {
	// Reset Silence Stage on legitimate input (not generated silence words)
	isSilenceWord := (text == "..." || text == "間" || text == "沈黙" || text == "静寂")

	// Semantic Categories
	conjunctions := []string{"でも", "しかし", "だが", "逆に", "とは言え", "けど", "反対に"}
	hesitations := []string{"えっと", "うーん", "あの", "多分", "かな", "なんか", "えー"}

	isConjunction := false
	isHesitation := false

	for _, c := range conjunctions {
		if strings.Contains(text, c) {
			isConjunction = true
			break
		}
	}
	for _, h := range hesitations {
		if strings.Contains(text, h) {
			isHesitation = true
			break
		}
	}

	if !isSilenceWord {
		g.silenceStage = 0

		// Update Target Background based on text
		if !glitch && g.state.CurrentState != "SPLIT" {
			if isConjunction {
				g.targetBgColor = color.RGBA{50, 50, 50, 255} // Grey out
			} else {
				g.targetBgColor = textToColor(text)
			}
		}
	}

	// Conversation Turn Logic (Only for real words)
	if !isSilenceWord {
		now := time.Now()
		silenceDuration := now.Sub(g.lastWordTime)
		g.lastWordTime = now

		// If silence > 2.0s, switch speaker
		if silenceDuration > 2000*time.Millisecond {
			g.currentSpeaker = (g.currentSpeaker + 1) % 2
		}

		// Conjunctions trigger immediate turn switch (interruption)
		if isConjunction {
			g.currentSpeaker = (g.currentSpeaker + 1) % 2
		}
	}

	// Physics Init
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
	var rot, vrot float64

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
		vy = 0.00                                 // Suspended in time (No movement)
		colorVal = color.RGBA{200, 200, 255, 200} // Bluish White
	} else if text == "沈黙" {
		scale = 5.0 // Massive
		life = 1000
		// Random X throughout screen (Avoid edges)
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = -100 // Drop from top
		vx = 0
		vy = 15.0                              // Heavy drop
		colorVal = color.RGBA{50, 50, 50, 255} // Dark Grey
	} else if text == "静寂" {
		scale = 7.0 // Even Bigger
		life = 1200 // Lasts longer
		// Random X throughout screen
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = float64(ScreenHeight) + 100 // Rise from Abyss
		vx = 0
		vy = -1.0                            // Slow Ascension (Rising Up)
		colorVal = color.RGBA{5, 5, 20, 255} // Deepest Blue/Black
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

		rot = (rand.Float64() - 0.5) * 0.5
		vrot = (rand.Float64() - 0.5) * 0.1

		// Apply Semantic Physics
		if isConjunction {
			vx *= -1.5           // REVERSE FLOW (Interruption)
			rot = math.Pi        // UPSIDE DOWN (Inversion)
			colorVal = ColYellow // Highlight
			scale *= 1.2
		}

		if isHesitation {
			vx *= 0.2                                 // Slow down
			vy *= 0.2                                 // Float
			vrot *= 0.05                              // No spin
			colorVal = color.RGBA{200, 200, 200, 150} // Transparent/Pale
			scale *= 0.8
		}
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
		Rotation:  rot,  // Use local var
		VRotation: vrot, // Use local var
		IsResting: false,
		IsFiller:  (len(text) <= 3 || isHesitation) && !isSilenceWord, // Simple check for fillers
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
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.jpFaceBig == nil {
		return
	}

	for i := range g.barrage {
		b := &g.barrage[i] // Pointer access to update Cache

		// Lazy Cache Image
		if b.Image == nil {
			rect := text.BoundString(g.jpFaceBig, b.Text)
			w := rect.Max.X - rect.Min.X + 4 // Padding
			h := rect.Max.Y - rect.Min.Y + 4
			if w <= 0 {
				w = 1
			}
			if h <= 0 {
				h = 1
			}

			img := ebiten.NewImage(w, h)
			// text.Draw draws starting at dot; need to shift by -Min
			text.Draw(img, b.Text, g.jpFaceBig, -rect.Min.X+2, -rect.Min.Y+2, b.Color)
			b.Image = img
		}

		// Glitch Effect: Random Jitter
		jx, jy := 0.0, 0.0
		if b.IsGlitch || g.state.CurrentState == "SPLIT" {
			jx = (rand.Float64() - 0.5) * 10
			jy = (rand.Float64() - 0.5) * 10
		}

		w, h := b.Image.Size()
		op := &ebiten.DrawImageOptions{}

		// Center Origin for Rotation/Scaling
		op.GeoM.Translate(float64(-w)/2, float64(-h)/2)

		// Apply Transformations
		scaleX := b.ScaleX
		if scaleX == 0 {
			scaleX = 1.0
		} // Safety
		op.GeoM.Scale(b.Scale*scaleX, b.Scale)

		// Apply Physics Rotation + Wave
		wave := 0.1 * math.Sin(float64(g.frameCount)*0.05)
		op.GeoM.Rotate(b.Rotation + wave)

		// Move to Position
		op.GeoM.Translate(b.X+jx, b.Y+jy)

		// Draw
		screen.DrawImage(b.Image, op)
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

	msgType, _ := data["type"].(string)

	if msgType == "spawn_word" {
		text, _ := data["text"].(string)
		// Pass entire data map as config
		g.spawnWordInternal(text, data)
	} else if msgType == "flash" {
		if word, ok := data["word"].(string); ok {
			g.spawnWordInternal(word, map[string]interface{}{"style": "glitch"})
		}
	} else {
		var newState State
		if err := json.Unmarshal(msg, &newState); err == nil {
			g.state = newState
		}
	}
}

// Public wrapper with Lock (safe for external calls)
func (g *Game) spawnWord(text string, glitch bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := map[string]interface{}{"style": "normal"}
	if glitch { cfg["style"] = "glitch" }
	g.spawnWordInternal(text, cfg)
}

// Internal implementation (Physics Executioner)
// Logic moved to Ruby. Go just executes Params.
func (g *Game) spawnWordInternal(text string, config map[string]interface{}) {
	style, _ := config["style"].(string)
	
	// Update Target Background
	if style != "glitch" && g.state.CurrentState != "SPLIT" {
		if style == "conjunction" {
			g.targetBgColor = color.RGBA{50, 50, 50, 255}
		} else {
			g.targetBgColor = textToColor(text)
		}
	}

	// Turn Logic
	if !strings.HasPrefix(style, "silence_") {
		now := time.Now()
		if now.Sub(g.lastWordTime) > 2000*time.Millisecond || style == "conjunction" {
			g.currentSpeaker = (g.currentSpeaker + 1) % 2
		}
		g.lastWordTime = now
	}

	// Physics Defaults
	nuanceScale := 1.0 + (g.micVolume * 3.0)
	if nuanceScale > 4.0 { nuanceScale = 4.0 }
	
	scale := nuanceScale + rand.Float64()*0.5
	life := 600
	colorVal := ColWhite
	scaleX := 1.0
	
	startX := 0.0
	startY := 0.0
	vx := 0.0
	vy := 0.0
	rot := (rand.Float64() - 0.5) * 0.5
	vrot := (rand.Float64() - 0.5) * 0.1

	// 1. Base Physics (Positioning)
	if style == "glitch" {
		scale *= 1.5
		startX = float64(ScreenWidth/2) + rand.Float64()*400 - 200
		vx = (rand.Float64() - 0.5) * 10
		vy = (rand.Float64() - 0.5) * 10
		colorVal = ColRed
		life = 300
	} else if style == "silence_dots" {
		scale = 1.0; life = 300
		startX = rand.Float64()*ScreenWidth; startY = rand.Float64()*ScreenHeight
		vx = (rand.Float64()-0.5)*0.5; vy = (rand.Float64()-0.5)*0.5
		colorVal = color.RGBA{100, 100, 100, 100}
	} else if style == "silence_ma" {
		scale = 3.0; life = 800
		startX = ScreenWidth/2; startY = ScreenHeight/3
		vx = 0; vy = 0
		colorVal = color.RGBA{200, 200, 255, 200}
	} else if style == "silence_heavy" {
		scale = 5.0; life = 1000
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = -100
		vx = 0; vy = 15.0
		colorVal = color.RGBA{50, 50, 50, 255}
	} else if style == "silence_abyss" {
		scale = 7.0; life = 1200
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = ScreenHeight + 100
		vx = 0; vy = -1.0
		colorVal = color.RGBA{5, 5, 20, 255}
	} else {
		// Normal / Conjunction / Invert
		if g.state.CurrentState == "SPLIT" {
			startX = float64(ScreenWidth/2) + rand.Float64()*400 - 200
			vx = (rand.Float64() - 0.5) * 10
		} else if g.currentSpeaker == 0 {
			startX = ScreenWidth*0.2 + rand.Float64()*100
			vx = 5.0 + rand.Float64()*5.0
		} else {
			startX = ScreenWidth*0.8 - rand.Float64()*100
			vx = -5.0 - rand.Float64()*5.0
		}
		startY = ScreenHeight*0.4 + rand.Float64()*200 - 100
		vy = -5.0 - rand.Float64()*5.0
	}

	// 2. Ruby Overrides (Explicit Physics)
	if val, ok := config["rot"].(float64); ok { rot = val }
	if val, ok := config["scalex"].(float64); ok { scaleX = val }
	if val, ok := config["vy"].(float64); ok { vy = val }
	if val, ok := config["vy_mult"].(float64); ok { vy *= val }
	if val, ok := config["scale"].(float64); ok { scale = val } // Added Override
	
	if colStr, ok := config["color"].(string); ok {
		switch colStr {
		case "cyan": colorVal = ColCyan
		case "yellow": colorVal = ColYellow
		case "grey": colorVal = color.RGBA{200, 200, 200, 150}
		case "dark_grey": colorVal = color.RGBA{50, 50, 50, 255}
		case "black": colorVal = color.RGBA{5, 5, 20, 255}
		case "blue_white": colorVal = color.RGBA{200, 200, 255, 200}
		case "grey_alpha": colorVal = color.RGBA{100, 100, 100, 100}
		}
	}

	bw := BarrageWord{
		Text:      text,
		X:         startX,
		Y:         startY,
		VX:        vx,
		VY:        vy,
		Scale:     scale,
		ScaleX:    scaleX,
		Color:     colorVal,
		Life:      life,
		MaxLife:   life,
		IsGlitch:  (style == "glitch"),
		Rotation:  rot,
		VRotation: vrot,
		IsResting: false,
		Image:     nil,
		IsFiller:  (len(text) <= 3) && !strings.HasPrefix(style, "silence_"),
	}

	if g.state.CurrentState == "SPLIT" || style == "glitch" {
		bw.Color = ColRed
		bw.VX *= 2.0
		bw.VY *= 2.0
	}

	g.barrage = append(g.barrage, bw)
}

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

					// 2. Local Atmosphere Logic (Fallback) - DISABLED (Migrated to Ruby)
					/*
						dangerWords := []string{"嘘", "矛盾", "違う", "だめ", "無理", "変", "おかしい", "バグ", "ミス", "否定", "NO", "嫌", "怖い"}
						isDanger := false
						for _, dw := range dangerWords {
							if strings.Contains(txt, dw) {
								isDanger = true
								break
							}
						}
						isGlitch := len(txt) < 3
						if isDanger { ... }
						game.spawnWord(txt, isGlitch)
					*/
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
