package quantize

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Eligibility is a conservative preflight, not runtime or quality certification.
type Eligibility struct {
	Eligible     bool   `json:"eligible"`
	Reason       string `json:"reason"`
	Architecture string `json:"architecture,omitempty"`
	Format       string `json:"format,omitempty"`
}

// InspectInput reads bounded GGUF metadata and tensor descriptors, not weights.
// The pinned quantizer remains the final authority for supported architectures.
func InspectInput(path string) (Eligibility, error) {
	file, err := os.Open(path)
	if err != nil {
		return Eligibility{}, err
	}
	defer file.Close()
	reader := &metadataReader{r: io.LimitReader(file, 16<<20)}
	header := make([]byte, 24)
	if err := reader.read(header); err != nil {
		return Eligibility{}, err
	}
	if string(header[:4]) != "GGUF" {
		return Eligibility{}, errors.New("not a GGUF file")
	}
	tensors := binary.LittleEndian.Uint64(header[8:16])
	count := binary.LittleEndian.Uint64(header[16:24])
	if count == 0 || count > 65536 || tensors == 0 || tensors > 1<<20 {
		return Eligibility{}, errors.New("GGUF metadata or tensor count is missing or exceeds the preflight limit")
	}
	var architecture, kind, format string
	var fileType uint32
	var hasFileType bool
	for i := uint64(0); i < count; i++ {
		key, err := reader.stringValue()
		if err != nil {
			return Eligibility{}, err
		}
		typ, err := reader.uint32()
		if err != nil {
			return Eligibility{}, err
		}
		if key == "general.file_type" && typ == 4 {
			fileType, err = reader.uint32()
			hasFileType = true
			if err != nil {
				return Eligibility{}, err
			}
		} else if (key == "general.architecture" || key == "general.type") && typ == 8 {
			value, err := reader.stringValue()
			if err != nil {
				return Eligibility{}, err
			}
			if key == "general.architecture" {
				architecture = value
			} else {
				kind = value
			}
		} else if err := reader.skipValue(typ); err != nil {
			return Eligibility{}, err
		}
	}
	if architecture == "" {
		return Eligibility{}, errors.New("GGUF does not declare general.architecture")
	}
	if strings.EqualFold(architecture, "clip") || strings.EqualFold(kind, "mmproj") {
		return Eligibility{Reason: "Multimodal projector, not main model weights; quantize a full-precision main-model GGUF instead", Architecture: architecture}, nil
	}
	if hasFileType && fileType != 0 && fileType != 1 && fileType != 32 {
		return Eligibility{Reason: "Already quantized (GGUF file type); use original full-precision weights", Architecture: architecture}, nil
	}
	for i := uint64(0); i < tensors; i++ {
		if _, err := reader.stringValue(); err != nil {
			return Eligibility{}, err
		}
		dimensions, err := reader.uint32()
		if err != nil {
			return Eligibility{}, err
		}
		if dimensions == 0 || dimensions > 8 {
			return Eligibility{}, errors.New("GGUF tensor has an invalid dimension count")
		}
		for j := uint32(0); j < dimensions; j++ {
			if _, err := reader.uint64(); err != nil {
				return Eligibility{}, err
			}
		}
		tensorType, err := reader.uint32()
		if err != nil {
			return Eligibility{}, err
		}
		if tensorType != 0 && tensorType != 1 && tensorType != 30 { // ggml F32, F16, BF16
			return Eligibility{Reason: "Already quantized or has unsupported tensor types; use original full-precision weights", Architecture: architecture}, nil
		}
		if _, err := reader.uint64(); err != nil { // tensor data offset
			return Eligibility{}, err
		}
		if tensorType == 30 {
			format = "BF16"
		} else if tensorType == 1 && format == "" {
			format = "F16"
		} else if format == "" {
			format = "F32"
		}
	}
	return Eligibility{Eligible: true, Reason: "Full-precision tensor types found; quantizer support still requires execution", Architecture: architecture, Format: format}, nil
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
	eligibility, err := InspectInput(path)
	if err != nil {
		return fmt.Errorf("cannot inspect quantization input: %w", err)
	}
	if !eligibility.Eligible {
		return errors.New(eligibility.Reason)
	}
	return nil
}
