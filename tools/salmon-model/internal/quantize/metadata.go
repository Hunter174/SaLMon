package quantize

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Read only bounded GGUF metadata for a quantization preflight. This is not a
// runtime compatibility probe: the pinned quantizer remains the final authority.
func inputKind(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	reader := &metadataReader{r: io.LimitReader(file, 16<<20)}
	header := make([]byte, 24)
	if err := reader.read(header); err != nil {
		return "", "", err
	}
	if string(header[:4]) != "GGUF" {
		return "", "", errors.New("not a GGUF file")
	}
	count := binary.LittleEndian.Uint64(header[16:24])
	if count == 0 || count > 65536 {
		return "", "", errors.New("GGUF metadata count is missing or exceeds the preflight limit")
	}
	var architecture, kind string
	for i := uint64(0); i < count; i++ {
		key, err := reader.stringValue()
		if err != nil {
			return "", "", err
		}
		typ, err := reader.uint32()
		if err != nil {
			return "", "", err
		}
		if (key == "general.architecture" || key == "general.type") && typ == 8 {
			value, err := reader.stringValue()
			if err != nil {
				return "", "", err
			}
			if key == "general.architecture" {
				architecture = value
			} else {
				kind = value
			}
		} else if err := reader.skipValue(typ); err != nil {
			return "", "", err
		}
	}
	if architecture == "" {
		return "", "", errors.New("GGUF does not declare general.architecture")
	}
	return architecture, kind, nil
}

type metadataReader struct{ r io.Reader }

func (m *metadataReader) read(data []byte) error {
	if _, err := io.ReadFull(m.r, data); err != nil {
		return fmt.Errorf("GGUF metadata is truncated or exceeds the 16 MiB preflight limit: %w", err)
	}
	return nil
}
func (m *metadataReader) uint32() (uint32, error) {
	var data [4]byte
	if err := m.read(data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}
func (m *metadataReader) uint64() (uint64, error) {
	var data [8]byte
	if err := m.read(data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(data[:]), nil
}
func (m *metadataReader) stringValue() (string, error) {
	length, err := m.uint64()
	if err != nil {
		return "", err
	}
	if length > 1<<20 {
		return "", errors.New("GGUF metadata string exceeds the 1 MiB preflight limit")
	}
	data := make([]byte, int(length))
	if err := m.read(data); err != nil {
		return "", err
	}
	return string(data), nil
}
func (m *metadataReader) skipValue(typ uint32) error {
	if typ == 8 {
		_, err := m.stringValue()
		return err
	}
	if typ == 9 {
		element, err := m.uint32()
		if err != nil {
			return err
		}
		count, err := m.uint64()
		if err != nil {
			return err
		}
		if count > 1<<20 || element == 9 {
			return errors.New("GGUF metadata array is unsupported or exceeds the preflight limit")
		}
		for i := uint64(0); i < count; i++ {
			if err := m.skipValue(element); err != nil {
				return err
			}
		}
		return nil
	}
	width := map[uint32]int64{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}[typ]
	if width == 0 {
		return fmt.Errorf("unsupported GGUF metadata value type %d", typ)
	}
	_, err := io.CopyN(io.Discard, m.r, width)
	if err != nil {
		return fmt.Errorf("GGUF metadata is truncated or exceeds the preflight limit: %w", err)
	}
	return nil
}

func checkQuantizableInput(path string) error {
	architecture, kind, err := inputKind(path)
	if err != nil {
		return fmt.Errorf("cannot inspect quantization input: %w", err)
	}
	if strings.EqualFold(architecture, "clip") || strings.EqualFold(kind, "mmproj") {
		return fmt.Errorf("GGUF is a multimodal projector (architecture %q, type %q), not a language-model weight file; quantize the main model GGUF instead", architecture, kind)
	}
	return nil
}
