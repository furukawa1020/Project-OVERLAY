package main

import (
	"strings"
	"time"
)

// Config mimicking Ruby's behavior
var DangerWords = []string{"矛盾", "ふざけるな", "嘘", "絶対", "違う", "変", "おかしい"}

type Brain struct {
	Tension        float64
	LastUpdate     time.Time
	LastSpeechTime time.Time
	SilenceStage   int
}

func NewBrain() *Brain {
	return &Brain{
		LastUpdate:     time.Now(),
		LastSpeechTime: time.Now(),
	}
}

type WordConfig struct {
	Text   string
	Style  string
	Scale  float64
	ScaleX float64
	Rot    float64
	Color  string
	VY     float64
	VYMult float64
	Flash  bool
	Shake  float64
}

// Default config
func NewWordConfig(text string) WordConfig {
	return WordConfig{
		Text:   text,
		Style:  "normal",
		Scale:  1.0,
		ScaleX: 1.0,
		Rot:    0.0,
		Color:  "white",
		VY:     0.0,
		VYMult: 1.0,
	}
}

func (b *Brain) ProcessText(text string) WordConfig {
	b.LastSpeechTime = time.Now()
	b.SilenceStage = 0

	// Tension
	hit := false
	for _, w := range DangerWords {
		if strings.Contains(text, w) {
			hit = true
			break
		}
	}

	if hit {
		b.Tension += 3.0
	} else {
		b.Tension += 0.2
	}

	b.Recalculate()
	return b.AnalyzeSemantics(text)
}

func (b *Brain) AnalyzeSemantics(text string) WordConfig {
	cfg := NewWordConfig(text)

	// Impact Logic
	impactWords := []string{"絶対", "嘘", "違う", "矛盾", "変", "おかしい"}
	for _, w := range impactWords {
		if strings.Contains(text, w) {
			cfg.Style = "impact"
			cfg.Flash = true
			cfg.Shake = 20.0
			cfg.Color = "red"
			cfg.Scale = 2.5
			return cfg
		}
	}

	// Inversion Logic
	if (strings.Contains(text, "上下") || strings.Contains(text, "天井") || strings.Contains(text, "逆さま")) &&
		(strings.Contains(text, "反転") || strings.Contains(text, "逆")) {
		cfg.Style = "invert_v"
		cfg.Rot = 3.14159
		cfg.VYMult = -1.0
		cfg.Color = "cyan"
		return cfg
	}

	if (strings.Contains(text, "左右") || strings.Contains(text, "鏡")) &&
		(strings.Contains(text, "反転") || strings.Contains(text, "逆")) {
		cfg.Style = "invert_h"
		cfg.ScaleX = -1.0
		cfg.Color = "cyan"
		return cfg
	}

	if (strings.Contains(text, "色") || strings.Contains(text, "カラー")) &&
		(strings.Contains(text, "反転") || strings.Contains(text, "違う")) {
		cfg.Style = "invert_c"
		cfg.Color = "cyan"
		return cfg
	}

	// Conjunctions
	conjunctions := []string{"でも", "しかし", "だが", "逆に", "とは言え", "けど", "反対に"}
	for _, w := range conjunctions {
		if strings.Contains(text, w) {
			cfg.Style = "conjunction"
			cfg.ScaleX = -1.0
			cfg.Rot = 3.14159
			cfg.Color = "yellow"
			return cfg
		}
	}

	// Hesitation
	hesitations := []string{"えっと", "うーん", "あの", "多分", "かな", "なんか", "えー"}
	for _, w := range hesitations {
		if strings.Contains(text, w) {
			cfg.Style = "hesitation"
			cfg.Color = "grey"
			return cfg
		}
	}

	return cfg
}

func (b *Brain) CheckSilence() (string, WordConfig, bool) {
	now := time.Now()
	duration := now.Sub(b.LastSpeechTime).Seconds()

	if duration > 2.0 && b.SilenceStage == 0 {
		b.SilenceStage = 1
		cfg := NewWordConfig("...")
		cfg.Style = "silence_dots"
		cfg.Color = "grey_alpha"
		cfg.Scale = 0.8
		return "...", cfg, true
	} else if duration > 5.0 && b.SilenceStage == 1 {
		b.SilenceStage = 2
		cfg := NewWordConfig("間")
		cfg.Style = "silence_ma"
		cfg.Color = "blue_white"
		cfg.Scale = 1.0
		return "間", cfg, true
	} else if duration > 8.0 && b.SilenceStage == 2 {
		b.SilenceStage = 3
		cfg := NewWordConfig("沈黙")
		cfg.Style = "silence_heavy"
		cfg.Color = "dark_grey"
		cfg.VY = 15.0
		cfg.Scale = 1.5
		return "沈黙", cfg, true
	} else if duration > 12.0 && b.SilenceStage == 3 {
		b.SilenceStage = 4
		cfg := NewWordConfig("静寂")
		cfg.Style = "silence_abyss"
		cfg.Color = "black"
		cfg.VY = -1.0
		cfg.Scale = 2.0
		return "静寂", cfg, true
	} else if duration > 17.0 && b.SilenceStage == 4 {
		// Loop
		b.LastSpeechTime = time.Now().Add(-12 * time.Second) // Set back to 12s mark
		cfg := NewWordConfig("...")
		cfg.Style = "silence_dots"
		cfg.Color = "grey_alpha"
		cfg.Scale = 1.0
		return "...", cfg, true
	}

	return "", WordConfig{}, false
}

func (b *Brain) Reset() {
	b.Tension = 0
	b.LastSpeechTime = time.Now()
	b.SilenceStage = 0
}

func (b *Brain) Recalculate() {
	now := time.Now()
	dt := now.Sub(b.LastUpdate).Seconds()
	b.LastUpdate = now

	// Decay
	b.Tension -= dt * 0.5
	if b.Tension < 0 {
		b.Tension = 0
	}
}

func (b *Brain) GetState() string {
	b.Recalculate()
	if b.Tension > 8.0 {
		return "SPLIT"
	} else if b.Tension > 2.0 {
		return "ALIGNED"
	}
	return "UNKNOWN"
}
