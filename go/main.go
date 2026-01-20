package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// Config
const (
	ScreenWidth  = 1920
	ScreenHeight = 1080
	ServerHost   = "localhost:4567" // Ruby server
)

// State from Server
type State struct {
	CurrentState string  `json:"state"` // ALIGNED, SPLIT, UNKNOWN
	SplitDegree  float64 `json:"split_degree"`
	Strength     float64 `json:"strength"`
	FlashWord    string  `json:"word,omitempty"` // From 'flash' event
}

// Game implements ebiten.Game interface.
type Game struct {
	conn      *websocket.Conn
	state     State
	flashWord string
	flashTTL  int
}

func (g *Game) Update() error {
	// Decrease Flash TTL
	if g.flashTTL > 0 {
		g.flashTTL--
		if g.flashTTL == 0 {
			g.flashWord = ""
		}
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// 1. Clear Screen (Black)
	screen.Fill(color.Black)

	// 2. Draw Floor (Line)
	g.drawFloor(screen)

	// 3. Draw Wall (Flash Word)
	g.drawWall(screen)
	
	// Debug Info
	ebitenutil.DebugPrint(screen, fmt.Sprintf("State: %s\nSplit: %.2f", g.state.CurrentState, g.state.SplitDegree))
}

func (g *Game) drawFloor(screen *ebiten.Image) {
	cx, cy := float32(ScreenWidth/2), float32(ScreenHeight/2)
	lineColor := color.RGBA{200, 200, 200, 255}
	thickness := float32(4.0)

	switch g.state.CurrentState {
	case "SPLIT":
		// Branching Y-shape
		degree := float32(g.state.SplitDegree) * 100 // Visual spread
		vector.StrokeLine(screen, cx, cy+400, cx, cy, thickness, lineColor, false)
		vector.StrokeLine(screen, cx, cy, cx-degree*2, cy-200, thickness, lineColor, false)
		vector.StrokeLine(screen, cx, cy, cx+degree*2, cy-200, thickness, lineColor, false)
	case "UNKNOWN":
		// Thin dotted line (simulated by alpha)
		col := color.RGBA{100, 100, 100, 100}
		vector.StrokeLine(screen, cx, cy+400, cx, cy-400, 2, col, false)
	default: // ALIGNED
		// Straight Line
		vector.StrokeLine(screen, cx, cy+400, cx, cy-400, thickness, lineColor, false)
	}
}

func (g *Game) drawWall(screen *ebiten.Image) {
	if g.flashWord != "" {
		// Simple text drawing (Debug print for now, needs proper font later)
		ebitenutil.DebugPrintAt(screen, g.flashWord, ScreenWidth/2, ScreenHeight/2)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return ScreenWidth, ScreenHeight
}

func (g *Game) connect() {
	u := url.URL{Scheme: "ws", Host: ServerHost, Path: "/cable"}
	log.Printf("Connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Println("Connection failed:", err)
		return
	}
	g.conn = c

	// Listen loop
	go func() {
		defer c.Close()
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}
			g.handleMessage(message)
		}
	}()
}

func (g *Game) handleMessage(msg []byte) {
	var data map[string]interface{}
	if err := json.Unmarshal(msg, &data); err != nil {
		return
	}

	if data["type"] == "flash" {
		if word, ok := data["word"].(string); ok {
			g.flashWord = word
			g.flashTTL = 60 // ~1 sec at 60fps
		}
	} else {
		// Assume State update
		// (Simple re-unmarshal to struct for convenience, though inefficient)
		json.Unmarshal(msg, &g.state)
	}
}

func main() {
	game := &Game{}
	game.connect()

	ebiten.SetWindowSize(ScreenWidth/2, ScreenHeight/2)
	ebiten.SetWindowTitle("OVERLAY Renderer")
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
