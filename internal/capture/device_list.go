//go:build cgo

package capture

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
)

// ListLoopbackDevices возвращает список имён доступных loopback-устройств (воспроизведение).
func ListLoopbackDevices() ([]string, error) {
	return listDevices(malgo.Loopback)
}

// ListCaptureDevices возвращает список имён доступных устройств захвата (микрофоны).
func ListCaptureDevices() ([]string, error) {
	return listDevices(malgo.Capture)
}

// ListAllDevices возвращает map[тип][]имя всех аудиоустройств.
func ListAllDevices() (map[string][]string, error) {
	result := make(map[string][]string)

	loopback, err := ListLoopbackDevices()
	if err != nil {
		return nil, fmt.Errorf("loopback: %w", err)
	}
	result["loopback"] = loopback

	capture, err := ListCaptureDevices()
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	result["capture"] = capture

	return result, nil
}

// ValidateDevice проверяет, существует ли устройство с указанным именем.
func ValidateDevice(kind malgo.DeviceType, name string) error {
	if name == "" {
		return nil // default device — always valid
	}

	devices, err := listDevices(kind)
	if err != nil {
		return fmt.Errorf("не удалось получить список устройств: %w", err)
	}

	for _, d := range devices {
		if d == name {
			return nil
		}
	}

	return fmt.Errorf("устройство %q не найдено среди %s-устройств. Доступные: %s",
		name, deviceKindName(kind), strings.Join(devices, ", "))
}

func listDevices(kind malgo.DeviceType) ([]string, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return nil, fmt.Errorf("malgo init: %w", err)
	}
	defer func() {
		ctx.Uninit()
		ctx.Free()
	}()

	devices, err := ctx.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("enumerate: %w", err)
	}

	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name()
	}
	return names, nil
}

func deviceKindName(kind malgo.DeviceType) string {
	switch kind {
	case malgo.Loopback:
		return "loopback (воспроизведение)"
	case malgo.Capture:
		return "capture (захват/микрофон)"
	default:
		return "audio"
	}
}
