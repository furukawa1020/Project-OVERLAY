package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"sync"

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
)

// Colors (Shaft Style)
var (
	ColRed    = color.RGBA{198, 40, 40, 255}
	ColBlack  = color.RGBA{10, 10, 10, 255}
	ColWhite  = color.RGBA{240, 240, 240, 255}
	ColYellow = color.RGBA{253, 216, 53, 255}
	ColCyan   = color.RGBA{0, 255, 255, 255}
)

type State struct {
	CurrentState string
}

type Game struct {
	mu        sync.RWMutex
	state     State
	jpFace    font.Face
	jpFaceBig font.Face

	// Logic
	brain *Brain

	// Audio
	speech    *SpeechEngine
	audioChan chan float64

	micVolume  float64 // 0.0 - 1.0 (Smoothed)
	peakVolume float64

	// Visuals
	frameCount  int
	videoGlitch float64 // For Shaft cut effect
	words       []string
	barrage     []BarrageWord

	// Effects
	shakeAmount    float64
	flashIntensity float64
	gears          []Gear

	// Synesthetic state
	bgColor       color.RGBA
	targetBgColor color.RGBA

	// Conversation State
	// Handled by Brain now
	currentSpeaker int // 0: Left, 1: Right
	lastWordTime   time.Time
}

type Gear struct {
	X, Y, Radius, Rotation, Speed float64
	Teeth                         int
	Color                         color.RGBA
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

	// Physics
	Rotation  float64
	VRotation float64
	IsResting bool
	IsFiller  bool

	// Visual Cache
	Image  *ebiten.Image
	ScaleX float64
}

func (g *Game) Update() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.frameCount++

	// Init Gears (Lazy)
	if len(g.gears) == 0 {
		g.initGears()
	}

	// 1. Audio Processing
	if g.audioChan == nil {
		g.audioChan = make(chan float64, 10)
	}
	select {
	case vol := <-g.audioChan:
		target := vol * 8.0
		if target > g.micVolume {
			g.micVolume = target
		} else {
			g.micVolume *= 0.92
		}
	default:
		g.micVolume *= 0.95
	}

	// 2. Consume Speech (Brain Input)
	select {
	case text := <-g.speech.TextChan:
		// Process via Brain
		cfg := g.brain.ProcessText(text)
		g.spawnWordFromConfig(cfg)
	default:
		// No speech
	}

	// 3. Check Silence (Brain Loop)
	if word, cfg, ok := g.brain.CheckSilence(); ok {
		g.spawnWordFromConfig(cfg)
	}

	// 4. Update State
	g.state.CurrentState = g.brain.GetState()

	// 5. Update Physics & Effects
	g.updatePhysics()

	return nil
}

func (g *Game) updatePhysics() {
	// Decay Effects
	g.shakeAmount *= 0.9
	if g.shakeAmount < 0.5 {
		g.shakeAmount = 0
	}
	g.flashIntensity *= 0.85

	// Rotate Gears
	for i := range g.gears {
		g.gears[i].Rotation += g.gears[i].Speed
	}

	// Update Barrage
	newBarrage := []BarrageWord{}
	gravity := 0.25
	floorY := float64(ScreenHeight) - 100.0

	for _, b := range g.barrage {
		if !b.IsResting {
			grav := gravity
			if b.IsFiller {
				grav *= 0.2
			}

			b.VY += grav
			b.X += b.VX
			b.Y += b.VY
			b.Rotation += b.VRotation

			b.VX *= 0.98
			b.VRotation *= 0.98

			if b.Y > floorY {
				b.Y = floorY
				b.VY *= -0.6
				b.VX *= 0.8
				if math.Abs(b.VY) < 1.0 {
					b.IsResting = true
					b.VY = 0
				}
			}

			if b.X < 50 || b.X > ScreenWidth-50 {
				b.VX *= -0.8
				b.X += b.VX
			}
		}

		b.Life--
		if b.Life > 0 {
			newBarrage = append(newBarrage, b)
		}
	}
	g.barrage = newBarrage

	// Color Logic
	if g.state.CurrentState == "SPLIT" {
		g.targetBgColor = ColRed
	}
	g.bgColor = lerpColor(g.bgColor, g.targetBgColor, 0.05)
}

func (g *Game) spawnWordFromConfig(cfg WordConfig) {
	// Turn Logic (Simplified)
	if !strings.HasPrefix(cfg.Style, "silence_") {
		now := time.Now()
		if now.Sub(g.lastWordTime) > 2000*time.Millisecond || cfg.Style == "conjunction" {
			g.currentSpeaker = (g.currentSpeaker + 1) % 2
		}
		g.lastWordTime = now
	}

	// Apply Config
	style := cfg.Style
	text := cfg.Text

	if style != "glitch" && style != "impact" && g.state.CurrentState != "SPLIT" {
		if style == "conjunction" {
			g.targetBgColor = color.RGBA{50, 50, 50, 255}
		} else {
			g.targetBgColor = textToColor(text)
		}
	}

	// Physics Defaults
	nuanceScale := 1.0 + (g.micVolume * 3.0)
	if nuanceScale > 4.0 {
		nuanceScale = 4.0
	}

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

	// Base Positioning
	if style == "glitch" || style == "impact" {
		scale *= 1.5
		startX = float64(ScreenWidth/2) + rand.Float64()*400 - 200
		vx = (rand.Float64() - 0.5) * 10
		vy = (rand.Float64() - 0.5) * 10
		colorVal = ColRed
		life = 300
	} else if style == "silence_dots" {
		scale = 1.0
		life = 300
		startX = rand.Float64() * ScreenWidth
		startY = rand.Float64() * ScreenHeight
		vx = (rand.Float64() - 0.5) * 0.5
		vy = (rand.Float64() - 0.5) * 0.5
		colorVal = color.RGBA{100, 100, 100, 100}
	} else if style == "silence_ma" {
		scale = 3.0
		life = 800
		startX = ScreenWidth / 2
		startY = ScreenHeight / 3
		vx = 0
		vy = 0
		colorVal = color.RGBA{200, 200, 255, 200}
	} else if style == "silence_heavy" {
		scale = 5.0
		life = 1000
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = -100
		vx = 0
		vy = 15.0
		colorVal = color.RGBA{50, 50, 50, 255}
	} else if style == "silence_abyss" {
		scale = 7.0
		life = 1200
		startX = 100 + rand.Float64()*(ScreenWidth-200)
		startY = ScreenHeight + 100
		vx = 0
		vy = -1.0
		colorVal = color.RGBA{5, 5, 20, 255}
	} else {
		// Normal
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

	// Apply Overrides from Config
	if cfg.Rot != 0 {
		rot = cfg.Rot
	}
	if cfg.ScaleX != 1.0 { // Default is 1.0
		scaleX = cfg.ScaleX
	}
	if cfg.VY != 0 {
		vy = cfg.VY
	}
	if cfg.VYMult != 1.0 {
		vy *= cfg.VYMult
	}
	if cfg.Scale != 1.0 {
		scale = cfg.Scale
	}

	// Color String to Color
	switch cfg.Color {
	case "cyan":
		colorVal = ColCyan
	case "red":
		colorVal = ColRed
	case "yellow":
		colorVal = ColYellow
	case "grey":
		colorVal = color.RGBA{200, 200, 200, 150}
	case "dark_grey":
		colorVal = color.RGBA{50, 50, 50, 255}
	case "black":
		colorVal = color.RGBA{5, 5, 20, 255}
	case "blue_white":
		colorVal = color.RGBA{200, 200, 255, 200}
	case "grey_alpha":
		colorVal = color.RGBA{100, 100, 100, 100}
	}

	// Effects
	if cfg.Shake > 0 {
		g.shakeAmount += cfg.Shake
	}
	if cfg.Flash {
		g.flashIntensity = 1.0
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
		IsGlitch:  (style == "glitch" || style == "impact"),
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

func (g *Game) initGears() {
	g.gears = []Gear{
		{X: 100, Y: 100, Radius: 150, Teeth: 12, Speed: 0.005, Color: color.RGBA{40, 40, 40, 255}},
		{X: ScreenWidth - 100, Y: ScreenHeight - 150, Radius: 200, Teeth: 16, Speed: -0.003, Color: color.RGBA{30, 30, 30, 255}},
		{X: ScreenWidth / 2, Y: -100, Radius: 300, Teeth: 24, Speed: 0.002, Color: color.RGBA{20, 20, 20, 255}},
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	g.mu.RLock()
	currentState := g.state.CurrentState
	vol := g.micVolume
	shake := g.shakeAmount
	flash := g.flashIntensity
	g.mu.RUnlock()

	dx, dy := 0.0, 0.0
	if shake > 0 {
		dx = (rand.Float64() - 0.5) * shake
		dy = (rand.Float64() - 0.5) * shake
	}

	screen.Fill(g.bgColor)
	g.drawGears(screen, dx, dy)
	g.drawGeometry(screen, dx, dy)
	g.drawBarrage(screen, dx, dy)

	if flash > 0.01 {
		alpha := uint8(flash * 255)
		vector.DrawFilledRect(screen, 0, 0, float32(ScreenWidth), float32(ScreenHeight), color.RGBA{255, 255, 255, alpha}, true)
	}

	ebitenutil.DebugPrint(screen, fmt.Sprintf("Vol: %.2f | State: %s", vol, currentState))
}

func (g *Game) drawGears(screen *ebiten.Image, dx, dy float64) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, gear := range g.gears {
		cx, cy := float32(gear.X+dx), float32(gear.Y+dy)
		vector.DrawFilledCircle(screen, cx, cy, float32(gear.Radius), gear.Color, true)
		for i := 0; i < gear.Teeth; i++ {
			theta := gear.Rotation + (float64(i) / float64(gear.Teeth) * 2 * math.Pi)
			tx := cx + float32(math.Cos(theta))*float32(gear.Radius+20)
			ty := cy + float32(math.Sin(theta))*float32(gear.Radius+20)
			vector.StrokeLine(screen, cx, cy, tx, ty, 20, gear.Color, true)
		}
		vector.DrawFilledCircle(screen, cx, cy, float32(gear.Radius*0.3), g.bgColor, true)
	}
}

func (g *Game) drawGeometry(screen *ebiten.Image, dx, dy float64) {
	cx, cy := float32(ScreenWidth/2+dx), float32(ScreenHeight/2+dy)

	g.mu.RLock()
	micVolume := g.micVolume
	currentState := g.state.CurrentState
	g.mu.RUnlock()

	radius := float32(200.0 + micVolume*400.0)
	thickness := float32(2.0 + micVolume*10.0)
	theta := float64(g.frameCount) * 0.02

	x1 := cx + float32(math.Cos(theta))*radius
	y1 := cy + float32(math.Sin(theta))*radius
	x2 := cx - float32(math.Cos(theta))*radius
	y2 := cy - float32(math.Sin(theta))*radius

	col := ColWhite
	if currentState == "SPLIT" {
		col = ColRed
		vector.StrokeLine(screen, x1+20, y1, x2+20, y2, thickness, col, true)
	}
	vector.StrokeLine(screen, x1, y1, x2, y2, thickness, col, true)
}

func (g *Game) drawBarrage(screen *ebiten.Image, dx, dy float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.jpFaceBig == nil {
		return
	}

	for i := range g.barrage {
		b := &g.barrage[i]
		if b.Image == nil {
			rect := text.BoundString(g.jpFaceBig, b.Text)
			w := rect.Max.X - rect.Min.X + 4
			h := rect.Max.Y - rect.Min.Y + 4
			if w <= 0 {
				w = 1
			}
			if h <= 0 {
				h = 1
			}
			img := ebiten.NewImage(w, h)
			text.Draw(img, b.Text, g.jpFaceBig, -rect.Min.X+2, -rect.Min.Y+2, b.Color)
			b.Image = img
		}

		jx, jy := 0.0, 0.0
		if b.IsGlitch || g.state.CurrentState == "SPLIT" {
			jx = (rand.Float64() - 0.5) * 10
			jy = (rand.Float64() - 0.5) * 10
		}

		w, h := b.Image.Size()
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(float64(-w)/2, float64(-h)/2)

		scaleX := b.ScaleX
		if scaleX == 0 {
			scaleX = 1.0
		}
		op.GeoM.Scale(b.Scale*scaleX, b.Scale)

		wave := 0.1 * math.Sin(float64(g.frameCount)*0.05)
		op.GeoM.Rotate(b.Rotation + wave)
		op.GeoM.Translate(b.X+jx+dx, b.Y+jy+dy)

		screen.DrawImage(b.Image, op)
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return ScreenWidth, ScreenHeight
}

func main() {
	game := &Game{}
	game.brain = NewBrain()

	// Load Fonts
	tt, err := opentype.Parse(mustReadFile("assets/font.otf"))
	if err != nil {
		tt, err = opentype.Parse(mustReadFile("C:\\Windows\\Fonts\\meiryo.ttc"))
		if err != nil {
			log.Fatal(err)
		}
	}

	const dpi = 72
	game.jpFace, _ = opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    24,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
	game.jpFaceBig, _ = opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    72,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})

	// Audio Init
	game.speech = NewSpeechEngine()
	game.speech.Start()
	game.audioChan = game.speech.VolChan

	ebiten.SetWindowSize(ScreenWidth, ScreenHeight)
	ebiten.SetWindowTitle("脳内劇場")
	ebiten.SetWindowFloating(true)
	ebiten.SetWindowDecorated(false)
	ebiten.SetScreenTransparent(true)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
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
	h := math.Abs(float64(hash % 360))
	s := 0.8
	v := 0.2
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

func mustReadFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
