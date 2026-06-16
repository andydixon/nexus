package spectrogram

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestImageToWAVAndRenderSpectrogram(t *testing.T) {
	img := testPNG(t)
	wav, summary, err := ImageToWAV(img, EncodeOptions{
		DurationSec: 0.25,
		SampleRate:  8000,
		MinHz:       300,
		MaxHz:       3000,
		Bins:        32,
		Columns:     32,
		Invert:      true,
	})
	if err != nil {
		t.Fatalf("ImageToWAV failed: %v", err)
	}
	if !isWAV(wav) {
		t.Fatalf("expected WAV header")
	}
	if summary["wavBytes"].(int) != len(wav) {
		t.Fatalf("summary wavBytes mismatch")
	}

	rendered, viewSummary, err := AudioSpectrogramPNG(wav, "test.wav", ViewOptions{
		FFTSize:  512,
		Height:   96,
		Aspect:   2,
		MaxWidth: 128,
	})
	if err != nil {
		t.Fatalf("AudioSpectrogramPNG failed: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(rendered)); err != nil {
		t.Fatalf("rendered spectrogram is not a PNG: %v", err)
	}
	if viewSummary["sampleRate"].(int) != 8000 {
		t.Fatalf("unexpected sample rate: %v", viewSummary["sampleRate"])
	}
	if viewSummary["height"].(int) != 192 {
		t.Fatalf("expected aspect-adjusted height 192, got %v", viewSummary["height"])
	}
}

func TestPreserveAspectRatioAdjustsFrequencyRange(t *testing.T) {
	img := testPNGSize(t, 32, 16)
	_, summary, err := ImageToWAV(img, EncodeOptions{
		DurationSec: 0.1,
		SampleRate:  8000,
		MinHz:       200,
		MaxHz:       3000,
		Bins:        16,
		Columns:     16,
		PreserveAR:  true,
		Invert:      true,
	})
	if err != nil {
		t.Fatalf("ImageToWAV failed: %v", err)
	}
	if summary["preserveAR"] != true {
		t.Fatalf("expected preserveAR summary")
	}
	if got := summary["maxHz"].(float64); got != 1600 {
		t.Fatalf("expected landscape image to halve frequency range to 1600Hz, got %v", got)
	}
}

func TestImageToWAVRejectsOversizedImageDimensionsBeforeDecode(t *testing.T) {
	_, _, err := ImageToWAV(hugePNGHeader(t, 100000, 100000), EncodeOptions{})
	if err == nil {
		t.Fatal("expected oversized image dimensions to be rejected")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("image dimensions are too large")) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertImageIntoAudioExtendsCarrier(t *testing.T) {
	img := testPNG(t)
	carrier, _, err := ImageToWAV(img, EncodeOptions{
		DurationSec: 0.1,
		SampleRate:  8000,
		MinHz:       300,
		MaxHz:       2500,
		Bins:        16,
		Columns:     16,
		Invert:      true,
	})
	if err != nil {
		t.Fatalf("carrier generation failed: %v", err)
	}
	out, summary, err := InsertImageIntoAudio(img, carrier, "carrier.wav", InsertOptions{
		EncodeOptions: EncodeOptions{
			DurationSec: 0.2,
			MinHz:       300,
			MaxHz:       2500,
			Bins:        16,
			Columns:     16,
			Invert:      true,
		},
		StartSec: 0.05,
		MixLevel: 0.25,
	})
	if err != nil {
		t.Fatalf("InsertImageIntoAudio failed: %v", err)
	}
	if !isWAV(out) {
		t.Fatalf("expected WAV output")
	}
	if summary["mode"] != "insert-into-audio" {
		t.Fatalf("unexpected mode: %v", summary["mode"])
	}
	if len(out) <= len(carrier) {
		t.Fatalf("expected carrier to be extended")
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	return testPNGSize(t, 32, 32)
}

func testPNGSize(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	limit := width
	if height < limit {
		limit = height
	}
	for i := 0; i < limit; i++ {
		img.Set(i, i, color.RGBA{A: 255})
		img.Set(width-1-i, i, color.RGBA{A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func hugePNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8
	ihdr[9] = 2
	writePNGChunk(&buf, "IHDR", ihdr)
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, kind string, data []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(data)))
	buf.WriteString(kind)
	buf.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(data)
	_ = binary.Write(buf, binary.BigEndian, crc.Sum32())
}
