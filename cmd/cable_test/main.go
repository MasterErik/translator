//go:build cgo

// Cable Test — проверка виртуального аудиоканала VB-Cable.
//   - Выводит список loopback и capture устройств
//   - Проверяет наличие CABLE Input (loopback) и CABLE Output (capture)
//   - Захватывает 5 секунд тишины через loopback для проверки инициализации
//
// Использование: go run ./cmd/cable_test
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/mastererik/translator/internal/capture"
)

func main() {
	fmt.Println("=== VB-Cable Test ===")
	fmt.Println()

	// 1. Вывести все loopback-устройства.
	fmt.Println("--- Loopback-устройства (WASAPI Loopback на Playback) ---")
	loopbackDevs, err := capture.ListLoopbackDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ОШИБКА: %v\n", err)
		os.Exit(1)
	}
	cableInputFound := false
	for i, name := range loopbackDevs {
		marker := " "
		if name == "CABLE Input (VB-Audio Virtual Cable)" {
			marker = "★"
			cableInputFound = true
		}
		fmt.Printf("  %s %d. %s\n", marker, i+1, name)
	}
	fmt.Println()

	// 2. Вывести все capture-устройства.
	fmt.Println("--- Capture-устройства (микрофоны / запись) ---")
	captureDevs, err := capture.ListCaptureDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ОШИБКА: %v\n", err)
		os.Exit(1)
	}
	cableOutputFound := false
	for i, name := range captureDevs {
		marker := " "
		if name == "CABLE Output (VB-Audio Virtual Cable)" {
			marker = "★"
			cableOutputFound = true
		}
		fmt.Printf("  %s %d. %s\n", marker, i+1, name)
	}
	fmt.Println()

	// 3. Проверить VB-Cable.
	fmt.Println("--- Проверка VB-Cable ---")
	if !cableInputFound && !cableOutputFound {
		fmt.Println("  РЕЗУЛЬТАТ: VB-Cable НЕ НАЙДЕН")
		fmt.Println("  Установите: https://vb-audio.com/Cable/")
		os.Exit(1)
	}

	if cableInputFound {
		fmt.Println("  ★ CABLE Input найден как Playback-устройство (loopback)")
	}
	if cableOutputFound {
		fmt.Println("  ★ CABLE Output найден как Recording-устройство (захват)")
	}
	fmt.Println()

	// 4. Объяснение архитектуры.
	fmt.Println("--- Как работает VB-Cable ---")
	fmt.Println("  CABLE Input  = Playback (Chrome/Teams звук → СЮДА)")
	fmt.Println("  CABLE Output = Recording (ОТСЮДА → захват приложением)")
	fmt.Println()
	fmt.Println("  Для Translator:")
	if cableInputFound {
		fmt.Println("  • LOOPBACK_DEVICE=\"CABLE Input (VB-Audio Virtual Cable)\"")
		fmt.Println("    → WASAPI loopback захватывает звук, идущий в CABLE Input")
	}
	if cableOutputFound {
		fmt.Println("  • MIC_DEVICE=\"CABLE Output (VB-Audio Virtual Cable)\"")
		fmt.Println("    → Альтернативно: захват CABLE Output как микрофона")
	}
	fmt.Println()

	// 5. Пробный захват через CABLE Input (loopback).
	if !cableInputFound {
		fmt.Println("--- Пробный захват: пропущен (CABLE Input не найден) ---")
		fmt.Println()
		fmt.Println("=== VB-Cable Test: Частично (только CABLE Output) ===")
		return
	}

	cableName := "CABLE Input (VB-Audio Virtual Cable)"
	fmt.Printf("--- Пробный захват через loopback: %q (5 секунд) ---\n", cableName)

	allocCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(msg string) {})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ОШИБКА инициализации: %v\n", err)
		os.Exit(1)
	}
	defer allocCtx.Uninit()
	defer allocCtx.Free()

	// Находим устройство по имени.
	devices, err := allocCtx.Devices(malgo.Loopback)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ОШИБКА перечисления: %v\n", err)
		os.Exit(1)
	}

	var deviceID *malgo.DeviceID
	for _, d := range devices {
		if d.Name() == cableName {
			deviceID = &d.ID
			break
		}
	}
	if deviceID == nil {
		fmt.Fprintf(os.Stderr, "  ОШИБКА: устройство не найдено при повторном перечислении\n")
		fmt.Println("  (это баг — устройство было в списке но исчезло)")
		os.Exit(1)
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Loopback)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 2
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInMilliseconds = 20

	if deviceID != nil {
		deviceConfig.Capture.DeviceID = deviceID.Pointer()
	}

	frameCount := 0
	callbacks := malgo.DeviceCallbacks{
		Data: func(output, input []byte, framecount uint32) {
			frameCount++
		},
	}

	device, err := malgo.InitDevice(allocCtx.Context, deviceConfig, callbacks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ОШИБКА создания устройства: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  Запуск захвата...\n")
	if err := device.Start(); err != nil {
		device.Uninit()
		fmt.Fprintf(os.Stderr, "  ОШИБКА запуска: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  Захват идёт 5 секунд...\n")
	time.Sleep(5 * time.Second)

	device.Stop()
	device.Uninit()

	fmt.Printf("  Захвачено кадров: %d (~%.1f секунд)\n", frameCount, float64(frameCount)*20.0/1000.0)
	fmt.Printf("  РЕЗУЛЬТАТ: OK — CABLE Input loopback работает\n")
	fmt.Println()
	fmt.Println("=== VB-Cable Test: ВСЁ ОК ===")
}
