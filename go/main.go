package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io/ioutil"
	"log"
	"net/url"

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

// State from Server
type State struct {
	CurrentState string  `json:"state"` // ALIGNED, SPLIT, UNKNOWN
	SplitDegree  float64 `json:"split_degree"`
	Strength     float64 `json:"strength"`
	FlashWord    string  `json:"word,omitempty"`
}

type Game struct {
	conn         *websocket.Conn
	targetState  State // Serverからの最新
	currentState State // 描画用の現在値（補間用）

	flashWord    string
	flashTTL     int     // Max 60
	flashOpacity float64 // 1.0 -> 0.0

	jpFace font.Face
}

func (g *Game) Update() error {
	// State Interpolation (Smooth transition)
	// SplitDegreeを滑らかに追従させる
	g.currentState.SplitDegree = lerp(g.currentState.SplitDegree, g.targetState.SplitDegree, 0.05)

	// Flash Logic
	if g.flashTTL > 0 {
		g.flashTTL--
		g.flashOpacity = float64(g.flashTTL) / 60.0
		if g.flashTTL == 0 {
			g.flashWord = ""
		}
	}
	return nil
}

func lerp(current, target, rate float64) float64 {
	return current + (target-current)*rate
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.Black)
	g.drawFloor(screen)
	g.drawWall(screen)

	// Debug
	ebitenutil.DebugPrint(screen, fmt.Sprintf("State: %s\nSplit: %.3f", g.targetState.CurrentState, g.currentState.SplitDegree))
}

func (g *Game) drawFloor(screen *ebiten.Image) {
	cx, cy := float32(ScreenWidth/2), float32(ScreenHeight/2)
	baseColor := color.RGBA{220, 220, 220, 255}

	// 状態に応じた線の表現
	// SplitDegreeが大きいほど、Y字が広がる

	thickness := float32(6.0)
	spread := float32(g.currentState.SplitDegree) * 600.0 // 最大600px広がる

	// Root line (手前から中心へ)
	vector.StrokeLine(screen, cx, float32(ScreenHeight), cx, cy, thickness, baseColor, true)

	// Branches
	// 左分岐
	vector.StrokeLine(screen, cx, cy, cx-spread, cy-400, thickness, baseColor, true)
	// 右分岐
	vector.StrokeLine(screen, cx, cy, cx+spread, cy-400, thickness, baseColor, true)

	// 中央線（ALIGNED維持なら強く表示、SPLITなら薄くなる）
	centerAlpha := uint8(255 * (1.0 - g.currentState.SplitDegree))
	if centerAlpha > 0 {
		centerColor := color.RGBA{220, 220, 220, centerAlpha}
		vector.StrokeLine(screen, cx, cy, cx, cy-400, thickness, centerColor, true)
	}
}

func (g *Game) drawWall(screen *ebiten.Image) {
	if g.flashWord != "" && g.jpFace != nil {
		// Calculate position to center text
		bounds := text.BoundString(g.jpFace, g.flashWord)
		textWidth := bounds.Max.X - bounds.Min.X
		textHeight := bounds.Max.Y - bounds.Min.Y

		x := (ScreenWidth - textWidth) / 2
		y := (ScreenHeight + textHeight) / 2

		// Opacity color
		alpha := uint8(255 * g.flashOpacity)
		c := color.RGBA{255, 50, 50, alpha} // Red flash for danger words

		text.Draw(screen, g.flashWord, g.jpFace, x, y, c)
	} else if g.flashWord != "" {
		// Fallback if no font
		ebitenutil.DebugPrintAt(screen, g.flashWord, ScreenWidth/2, ScreenHeight/2)
	}
}

func (g *Game) Layout(w, h int) (int, int) {
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
			g.flashTTL = 90 // 1.5 sec
		}
	} else {
		json.Unmarshal(msg, &g.targetState)
	}
}

func loadFont() font.Face {
	// 簡易的にWindowsのメイリオなどを読み込むトライ
	// 本番ではアセットディレクトリに同梱推奨
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
		log.Println("No Japanese font found.")
		return nil
	}

	tt, err := opentype.Parse(fontData)
	if err != nil {
		log.Println("Parse font error:", err)
		return nil
	}

	face, err := opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    128, // Large text
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		log.Println("NewFace error:", err)
		return nil
	}
	return face
}

func main() {
	game := &Game{}
	game.jpFace = loadFont()
	game.connect()

	ebiten.SetWindowSize(ScreenWidth/2, ScreenHeight/2)
	ebiten.SetWindowTitle("OVERLAY Spatial Renderer")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
