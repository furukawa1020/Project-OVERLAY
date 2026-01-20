package main

import (
	"encoding/json"
	"log"
	"math"
	"unsafe"

	vosk "github.com/alphacep/vosk-api/go"
	"github.com/gen2brain/malgo"
)

// SpeechEngine handles STT
type SpeechEngine struct {
	model      *vosk.VoskModel
	recognizer *vosk.VoskRecognizer
	device     *malgo.Device

	TextChan chan string
	VolChan  chan float64
}

func NewSpeechEngine() *SpeechEngine {
	// Suppress Vosk logs
	vosk.SetLogLevel(-1)

	// LOAD SMALL MODEL (Temporary for testing while Big downloads)
	model, err := vosk.NewModel("vosk/model")
	if err != nil {
		log.Println("Vosk Model Error:", err)
		return nil
	}

	rec, err := vosk.NewRecognizer(model, 44100.0)
	if err != nil {
		log.Println("Vosk Recognizer Error:", err)
		return nil
	}

	return &SpeechEngine{
		model:      model,
		recognizer: rec,
		TextChan:   make(chan string, 10),
		VolChan:    make(chan float64, 10),
	}
}

func (se *SpeechEngine) Start() {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		log.Println("Audio Context Error:", err)
		return
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Duplex)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = 44100
	deviceConfig.Alsa.NoMMap = 1

	// Buffer for Vosk (Keep it reasonably sized)
	// malgo callbacks happen on a separate thread
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSample []byte, framecount uint32) {
			// 1. Calculate Volume (RMS)
			sum := 0.0
			// pInputSample is S16LE (2 bytes)
			// Process every sample
			sh := (*(*[]int16)(unsafe.Pointer(&pInputSample)))[:framecount]

			for _, v := range sh {
				val := float64(v) / 32768.0
				sum += val * val
			}
			rms := math.Sqrt(sum / float64(framecount))

			// Non-blocking send
			select {
			case se.VolChan <- rms:
			default:
			}

			// 2. Feed to Vosk
			// Vosk expects []byte directly
			if se.recognizer.AcceptWaveform(pInputSample) != 0 {
				var res map[string]string
				json.Unmarshal([]byte(se.recognizer.Result()), &res)
				if txt := res["text"]; txt != "" {
					// Clean up spaces (Vosk adds spaces between words)
					// Japanese doesn't usually need them
					se.TextChan <- txt
				}
			} else {
				// Partial results? (Optional, maybe too noisy for this visual style)
				// var partial map[string]string
				// json.Unmarshal([]byte(se.recognizer.PartialResult()), &partial)
			}
		},
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		log.Println("Audio Device Error:", err)
		return
	}
	se.device = device

	if err := device.Start(); err != nil {
		log.Println("Audio Start Error:", err)
	}
}

func (se *SpeechEngine) Close() {
	if se.device != nil {
		se.device.Uninit()
	}
	// Vosk cleaning is manual in Go bindings?
	// Binding usually uses runtime.SetFinalizer, but good to check
}
