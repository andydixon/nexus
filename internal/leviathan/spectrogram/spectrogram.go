package spectrogram

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/hajimehoshi/go-mp3"
)

const (
	defaultSampleRate = 44100
	defaultDuration   = 5.0
	// MaxImageBytes caps compressed image inputs before decode. Pixel count is
	// checked separately because small compressed images can still expand huge.
	MaxImageBytes = 64 << 20
	// MaxAudioBytes caps encoded WAV/MP3 inputs before full in-memory decode.
	MaxAudioBytes   = 128 << 20
	maxImagePixels  = 24_000_000
	maxDecodedBytes = 64 << 20
	twoPi           = 2 * math.Pi
)

// EncodeOptions controls image-to-audio spectrogram synthesis.
type EncodeOptions struct {
	DurationSec float64
	SampleRate  int
	MinHz       float64
	MaxHz       float64
	Bins        int
	Columns     int
	Invert      bool
	PreserveAR  bool
	Gain        float64
}

// InsertOptions controls embedding synthesized spectrogram audio into a carrier.
type InsertOptions struct {
	EncodeOptions
	StartSec float64
	MixLevel float64
}

// ViewOptions controls spectrogram image rendering from audio.
type ViewOptions struct {
	FFTSize    int
	Height     int
	MaxWidth   int
	Aspect     float64
	MinHz      float64
	MaxHz      float64
	Brightness float64
	Contrast   float64
}

type audioBuffer struct {
	SampleRate int
	Channels   int
	Samples    []float64
}

// ImageToWAV converts an image into a WAV whose spectrogram reveals that image.
func ImageToWAV(imageData []byte, opts EncodeOptions) ([]byte, map[string]any, error) {
	grid, meta, opts, err := imageGrid(imageData, opts)
	if err != nil {
		return nil, nil, err
	}
	mono := synthesize(grid, opts)
	audio := audioBuffer{SampleRate: opts.SampleRate, Channels: 1, Samples: mono}
	wav, err := encodeWAV(audio)
	if err != nil {
		return nil, nil, err
	}
	summary := map[string]any{
		"mode":        "image-to-wav",
		"imageFormat": meta["format"],
		"imageWidth":  meta["width"],
		"imageHeight": meta["height"],
		"imageAspect": meta["aspect"],
		"durationSec": opts.DurationSec,
		"sampleRate":  opts.SampleRate,
		"minHz":       opts.MinHz,
		"maxHz":       opts.MaxHz,
		"bins":        opts.Bins,
		"columns":     opts.Columns,
		"preserveAR":  opts.PreserveAR,
		"wavBytes":    len(wav),
	}
	return wav, summary, nil
}

// InsertImageIntoAudio mixes a synthesized image spectrogram into WAV or MP3 carrier audio.
func InsertImageIntoAudio(imageData, audioData []byte, audioName string, opts InsertOptions) ([]byte, map[string]any, error) {
	carrier, err := DecodeAudio(audioData, audioName)
	if err != nil {
		return nil, nil, err
	}
	opts.EncodeOptions.SampleRate = carrier.SampleRate
	grid, meta, encodeOpts, err := imageGrid(imageData, opts.EncodeOptions)
	if err != nil {
		return nil, nil, err
	}
	tone := synthesize(grid, encodeOpts)
	if opts.MixLevel <= 0 {
		opts.MixLevel = 0.35
	}
	if opts.MixLevel > 1 {
		opts.MixLevel = 1
	}
	if opts.StartSec < 0 {
		opts.StartSec = 0
	}
	startFrame := int(math.Round(opts.StartSec * float64(carrier.SampleRate)))
	requiredFrames := startFrame + len(tone)
	currentFrames := len(carrier.Samples) / carrier.Channels
	if requiredFrames > currentFrames {
		extended := make([]float64, requiredFrames*carrier.Channels)
		copy(extended, carrier.Samples)
		carrier.Samples = extended
	}
	for i, sample := range tone {
		frame := startFrame + i
		for ch := 0; ch < carrier.Channels; ch++ {
			idx := frame*carrier.Channels + ch
			carrier.Samples[idx] += sample * opts.MixLevel
		}
	}
	limitPeak(carrier.Samples, 0.98)
	wav, err := encodeWAV(carrier)
	if err != nil {
		return nil, nil, err
	}
	summary := map[string]any{
		"mode":              "insert-into-audio",
		"imageFormat":       meta["format"],
		"imageWidth":        meta["width"],
		"imageHeight":       meta["height"],
		"imageAspect":       meta["aspect"],
		"carrierSampleRate": carrier.SampleRate,
		"carrierChannels":   carrier.Channels,
		"startSec":          opts.StartSec,
		"mixLevel":          opts.MixLevel,
		"durationSec":       encodeOpts.DurationSec,
		"minHz":             encodeOpts.MinHz,
		"maxHz":             encodeOpts.MaxHz,
		"bins":              encodeOpts.Bins,
		"columns":           encodeOpts.Columns,
		"preserveAR":        encodeOpts.PreserveAR,
		"wavBytes":          len(wav),
	}
	return wav, summary, nil
}

// AudioSpectrogramPNG renders WAV or MP3 audio as a PNG spectrogram.
func AudioSpectrogramPNG(audioData []byte, audioName string, opts ViewOptions) ([]byte, map[string]any, error) {
	audio, err := DecodeAudio(audioData, audioName)
	if err != nil {
		return nil, nil, err
	}
	mono := toMono(audio)
	if len(mono) == 0 {
		return nil, nil, errors.New("audio contains no samples")
	}
	opts = normalizeViewOptions(opts, audio.SampleRate)
	img := renderSpectrogram(mono, audio.SampleRate, opts)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil, err
	}
	duration := float64(len(mono)) / float64(audio.SampleRate)
	summary := map[string]any{
		"mode":         "audio-to-spectrogram",
		"sampleRate":   audio.SampleRate,
		"channels":     audio.Channels,
		"durationSec":  duration,
		"fftSize":      opts.FFTSize,
		"width":        img.Bounds().Dx(),
		"height":       img.Bounds().Dy(),
		"aspectRatio":  opts.Aspect,
		"dynamicRange": 80,
		"minHz":        opts.MinHz,
		"maxHz":        opts.MaxHz,
		"pngBytes":     buf.Len(),
	}
	return buf.Bytes(), summary, nil
}

// DecodeAudio decodes WAV directly and MP3 through go-mp3.
func DecodeAudio(data []byte, name string) (audioBuffer, error) {
	if len(data) == 0 {
		return audioBuffer{}, errors.New("audio file is empty")
	}
	if len(data) > MaxAudioBytes {
		return audioBuffer{}, fmt.Errorf("audio input is too large: %d bytes exceeds %d bytes", len(data), MaxAudioBytes)
	}
	if isWAV(data) {
		return decodeWAV(data)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".mp3" || isMP3(data) {
		return decodeMP3(data)
	}
	return audioBuffer{}, errors.New("unsupported audio format: load a WAV or MP3 file")
}

func imageGrid(data []byte, opts EncodeOptions) ([][]float64, map[string]any, EncodeOptions, error) {
	if len(data) == 0 {
		return nil, nil, opts, errors.New("image file is empty")
	}
	if len(data) > MaxImageBytes {
		return nil, nil, opts, fmt.Errorf("image input is too large: %d bytes exceeds %d bytes", len(data), MaxImageBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, opts, fmt.Errorf("decode image config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, nil, opts, errors.New("image has no pixels")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return nil, nil, opts, fmt.Errorf("image dimensions are too large: %dx%d exceeds %d pixels", cfg.Width, cfg.Height, maxImagePixels)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, opts, fmt.Errorf("decode image: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, nil, opts, errors.New("image has no pixels")
	}
	opts = normalizeEncodeOptions(opts, bounds.Dx(), bounds.Dy())
	grid := make([][]float64, opts.Bins)
	for bin := 0; bin < opts.Bins; bin++ {
		grid[bin] = make([]float64, opts.Columns)
		sourceY := bounds.Min.Y + int((float64(opts.Bins-1-bin)+0.5)*float64(bounds.Dy())/float64(opts.Bins))
		if sourceY >= bounds.Max.Y {
			sourceY = bounds.Max.Y - 1
		}
		for col := 0; col < opts.Columns; col++ {
			sourceX := bounds.Min.X + int((float64(col)+0.5)*float64(bounds.Dx())/float64(opts.Columns))
			if sourceX >= bounds.Max.X {
				sourceX = bounds.Max.X - 1
			}
			grid[bin][col] = pixelAmplitude(img.At(sourceX, sourceY), opts.Invert)
		}
	}
	meta := map[string]any{"format": format, "width": bounds.Dx(), "height": bounds.Dy(), "aspect": float64(bounds.Dy()) / float64(bounds.Dx())}
	return grid, meta, opts, nil
}

func normalizeEncodeOptions(opts EncodeOptions, imageWidth, imageHeight int) EncodeOptions {
	if opts.DurationSec <= 0 {
		opts.DurationSec = defaultDuration
	}
	opts.DurationSec = clampFloat(opts.DurationSec, 0.25, 60)
	if opts.SampleRate <= 0 {
		opts.SampleRate = defaultSampleRate
	}
	opts.SampleRate = clampInt(opts.SampleRate, 8000, 96000)
	if opts.MinHz <= 0 {
		opts.MinHz = 200
	}
	if opts.MaxHz <= opts.MinHz {
		opts.MaxHz = math.Min(float64(opts.SampleRate)/2-200, 20000)
	}
	opts.MinHz = clampFloat(opts.MinHz, 20, float64(opts.SampleRate)/2-100)
	opts.MaxHz = clampFloat(opts.MaxHz, opts.MinHz+100, float64(opts.SampleRate)/2-50)
	if opts.PreserveAR && imageWidth > 0 && imageHeight > 0 {
		aspect := float64(imageHeight) / float64(imageWidth)
		baseRange := opts.MaxHz - opts.MinHz
		desiredRange := baseRange * aspect
		opts.MaxHz = clampFloat(opts.MinHz+desiredRange, opts.MinHz+100, float64(opts.SampleRate)/2-50)
	}
	if opts.Bins <= 0 {
		opts.Bins = clampInt(imageHeight, 48, 192)
	}
	opts.Bins = clampInt(opts.Bins, 16, 384)
	if opts.Columns <= 0 {
		opts.Columns = clampInt(imageWidth, 64, 1024)
	}
	opts.Columns = clampInt(opts.Columns, 16, 2048)
	if opts.Gain <= 0 {
		opts.Gain = 0.85
	}
	opts.Gain = clampFloat(opts.Gain, 0.05, 1)
	return opts
}

func normalizeViewOptions(opts ViewOptions, sampleRate int) ViewOptions {
	if opts.FFTSize <= 0 {
		opts.FFTSize = 2048
	}
	opts.FFTSize = nearestPowerOfTwo(clampInt(opts.FFTSize, 256, 8192))
	if opts.Height <= 0 {
		opts.Height = 512
	}
	opts.Height = clampInt(opts.Height, 64, 1024)
	if opts.MaxWidth <= 0 {
		opts.MaxWidth = 1400
	}
	opts.MaxWidth = clampInt(opts.MaxWidth, 64, 2400)
	if opts.Aspect <= 0 {
		opts.Aspect = 1
	}
	opts.Aspect = clampFloat(opts.Aspect, 0.25, 4)
	if opts.MinHz < 0 {
		opts.MinHz = 0
	}
	if opts.MaxHz <= opts.MinHz {
		opts.MaxHz = float64(sampleRate) / 2
	}
	opts.MinHz = clampFloat(opts.MinHz, 0, float64(sampleRate)/2-10)
	opts.MaxHz = clampFloat(opts.MaxHz, opts.MinHz+10, float64(sampleRate)/2)
	if opts.Brightness <= 0 {
		opts.Brightness = 1
	}
	opts.Brightness = clampFloat(opts.Brightness, 0.1, 4)
	if opts.Contrast <= 0 {
		opts.Contrast = 1
	}
	opts.Contrast = clampFloat(opts.Contrast, 0.1, 4)
	return opts
}

func pixelAmplitude(c color.Color, invert bool) float64 {
	r, g, b, a := c.RGBA()
	alpha := float64(a) / 65535
	if alpha <= 0.001 {
		return 0
	}
	luma := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535
	amp := luma
	if invert {
		amp = 1 - luma
	}
	amp *= alpha
	return math.Pow(clampFloat(amp, 0, 1), 1.15)
}

func synthesize(grid [][]float64, opts EncodeOptions) []float64 {
	bins := len(grid)
	cols := len(grid[0])
	frames := int(math.Round(opts.DurationSec * float64(opts.SampleRate)))
	if frames < 1 {
		frames = 1
	}
	out := make([]float64, frames)
	phase := make([]float64, bins)
	inc := make([]float64, bins)
	for i := range inc {
		var pos float64
		if bins > 1 {
			pos = float64(i) / float64(bins-1)
		}
		freq := opts.MinHz + (opts.MaxHz-opts.MinHz)*pos
		inc[i] = twoPi * freq / float64(opts.SampleRate)
	}
	for i := 0; i < frames; i++ {
		var x float64
		if frames > 1 {
			x = float64(i) * float64(cols-1) / float64(frames-1)
		}
		c0 := int(math.Floor(x))
		c1 := c0 + 1
		if c1 >= cols {
			c1 = cols - 1
		}
		t := x - float64(c0)
		var sample, weight float64
		for bin := 0; bin < bins; bin++ {
			amp := grid[bin][c0]*(1-t) + grid[bin][c1]*t
			if amp < 0.002 {
				continue
			}
			phase[bin] += inc[bin]
			if phase[bin] > twoPi {
				phase[bin] -= twoPi
			}
			sample += math.Sin(phase[bin]) * amp
			weight += amp
		}
		if weight > 1 {
			sample /= math.Sqrt(weight)
		}
		out[i] = sample
	}
	normalizePeak(out, opts.Gain)
	fade(out, opts.SampleRate/100)
	return out
}

func renderSpectrogram(samples []float64, sampleRate int, opts ViewOptions) image.Image {
	fftSize := opts.FFTSize
	hop := fftSize / 4
	if hop < 1 {
		hop = 1
	}
	frameCount := 1
	if len(samples) > fftSize {
		frameCount = 1 + (len(samples)-fftSize)/hop
	}
	width := frameCount
	if width > opts.MaxWidth {
		width = opts.MaxWidth
	}
	if width < 1 {
		width = 1
	}
	height := clampInt(int(math.Round(float64(opts.Height)*opts.Aspect)), 32, 4096)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	values := make([]float32, width*height)
	maxDB := math.Inf(-1)
	window := hamming(fftSize)
	for x := 0; x < width; x++ {
		frameIndex := 0
		if width > 1 && frameCount > 1 {
			frameIndex = int(math.Round(float64(x) * float64(frameCount-1) / float64(width-1)))
		}
		start := frameIndex * hop
		buf := make([]complex128, fftSize)
		for i := 0; i < fftSize; i++ {
			if start+i < len(samples) {
				buf[i] = complex(samples[start+i]*window[i], 0)
			}
		}
		fft(buf)
		for y := 0; y < height; y++ {
			pos := 0.0
			if height > 1 {
				pos = float64(height-1-y) / float64(height-1)
			}
			freq := opts.MinHz + (opts.MaxHz-opts.MinHz)*pos
			bin := int(math.Round(freq * float64(fftSize) / float64(sampleRate)))
			if bin < 0 {
				bin = 0
			}
			if bin >= fftSize/2 {
				bin = fftSize/2 - 1
			}
			mag := cmplxAbs(buf[bin]) / float64(fftSize)
			db := 20 * math.Log10(mag+1e-10)
			values[y*width+x] = float32(db)
			if db > maxDB {
				maxDB = db
			}
		}
	}
	if math.IsInf(maxDB, -1) {
		maxDB = -80
	}
	minDB := maxDB - 80
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			db := float64(values[y*width+x])
			norm := clampFloat((db-minDB)/(maxDB-minDB), 0, 1)
			norm = clampFloat((norm-0.5)*opts.Contrast+0.5, 0, 1)
			norm = clampFloat(norm*opts.Brightness, 0, 1)
			img.Set(x, y, palette(norm))
		}
	}
	return img
}

func decodeWAV(data []byte) (audioBuffer, error) {
	if !isWAV(data) {
		return audioBuffer{}, errors.New("not a WAV file")
	}
	var fmtChunk, dataChunk []byte
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if size < 0 || pos+size > len(data) {
			return audioBuffer{}, errors.New("invalid WAV chunk size")
		}
		chunk := data[pos : pos+size]
		switch id {
		case "fmt ":
			fmtChunk = chunk
		case "data":
			dataChunk = chunk
		}
		pos += size
		if size%2 == 1 {
			pos++
		}
	}
	if len(fmtChunk) < 16 || len(dataChunk) == 0 {
		return audioBuffer{}, errors.New("WAV is missing fmt or data chunk")
	}
	audioFormat := binary.LittleEndian.Uint16(fmtChunk[0:2])
	channels := int(binary.LittleEndian.Uint16(fmtChunk[2:4]))
	sampleRate := int(binary.LittleEndian.Uint32(fmtChunk[4:8]))
	blockAlign := int(binary.LittleEndian.Uint16(fmtChunk[12:14]))
	bits := int(binary.LittleEndian.Uint16(fmtChunk[14:16]))
	if channels < 1 || channels > 8 || sampleRate < 1 || blockAlign < channels {
		return audioBuffer{}, errors.New("unsupported WAV stream parameters")
	}
	if len(dataChunk) > maxDecodedBytes {
		return audioBuffer{}, errors.New("audio data is too large to decode safely")
	}
	bytesPerSample := bits / 8
	if bytesPerSample < 1 || blockAlign < channels*bytesPerSample {
		return audioBuffer{}, errors.New("unsupported WAV bit depth")
	}
	frames := len(dataChunk) / blockAlign
	samples := make([]float64, frames*channels)
	for frame := 0; frame < frames; frame++ {
		base := frame * blockAlign
		for ch := 0; ch < channels; ch++ {
			off := base + ch*bytesPerSample
			samples[frame*channels+ch] = decodeWAVSample(dataChunk[off:], audioFormat, bits)
		}
	}
	return audioBuffer{SampleRate: sampleRate, Channels: channels, Samples: samples}, nil
}

func decodeWAVSample(b []byte, audioFormat uint16, bits int) float64 {
	switch audioFormat {
	case 1:
		switch bits {
		case 8:
			return (float64(b[0]) - 128) / 128
		case 16:
			return float64(int16(binary.LittleEndian.Uint16(b[:2]))) / 32768
		case 24:
			v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
			if v&0x800000 != 0 {
				v |= ^0xffffff
			}
			return float64(v) / 8388608
		case 32:
			return float64(int32(binary.LittleEndian.Uint32(b[:4]))) / 2147483648
		}
	case 3:
		switch bits {
		case 32:
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(b[:4])))
		case 64:
			return math.Float64frombits(binary.LittleEndian.Uint64(b[:8]))
		}
	}
	return 0
}

func decodeMP3(data []byte) (audioBuffer, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return audioBuffer{}, err
	}
	limited := io.LimitReader(decoder, maxDecodedBytes+1)
	pcm, err := io.ReadAll(limited)
	if err != nil {
		return audioBuffer{}, err
	}
	if len(pcm) > maxDecodedBytes {
		return audioBuffer{}, errors.New("decoded MP3 is too large to process safely")
	}
	if len(pcm) < 2 {
		return audioBuffer{}, errors.New("MP3 decoded no samples")
	}
	sampleCount := len(pcm) / 2
	channels := 2
	if sampleCount%2 != 0 {
		channels = 1
	}
	samples := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		samples[i] = float64(int16(binary.LittleEndian.Uint16(pcm[i*2:i*2+2]))) / 32768
	}
	return audioBuffer{SampleRate: decoder.SampleRate(), Channels: channels, Samples: samples}, nil
}

func encodeWAV(audio audioBuffer) ([]byte, error) {
	if audio.SampleRate <= 0 || audio.Channels <= 0 {
		return nil, errors.New("invalid audio parameters")
	}
	var buf bytes.Buffer
	dataSize := int64(len(audio.Samples)) * 2
	if dataSize > int64(^uint32(0))-44 {
		return nil, errors.New("audio is too large for a WAV file")
	}
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(audio.Channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(audio.SampleRate))
	byteRate := audio.SampleRate * audio.Channels * 2
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(audio.Channels*2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	for _, sample := range audio.Samples {
		sample = clampFloat(sample, -1, 1)
		_ = binary.Write(&buf, binary.LittleEndian, int16(math.Round(sample*32767)))
	}
	return buf.Bytes(), nil
}

func toMono(audio audioBuffer) []float64 {
	if audio.Channels == 1 {
		out := make([]float64, len(audio.Samples))
		copy(out, audio.Samples)
		return out
	}
	frames := len(audio.Samples) / audio.Channels
	out := make([]float64, frames)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		for ch := 0; ch < audio.Channels; ch++ {
			sum += audio.Samples[frame*audio.Channels+ch]
		}
		out[frame] = sum / float64(audio.Channels)
	}
	return out
}

func isWAV(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

func isMP3(data []byte) bool {
	return len(data) >= 3 && (string(data[:3]) == "ID3" || (data[0] == 0xff && data[1]&0xe0 == 0xe0))
}

func normalizePeak(samples []float64, target float64) {
	var peak float64
	for _, s := range samples {
		if a := math.Abs(s); a > peak {
			peak = a
		}
	}
	if peak <= 0 {
		return
	}
	scale := target / peak
	for i := range samples {
		samples[i] *= scale
	}
}

func limitPeak(samples []float64, target float64) {
	var peak float64
	for _, s := range samples {
		if a := math.Abs(s); a > peak {
			peak = a
		}
	}
	if peak <= target || peak <= 0 {
		return
	}
	scale := target / peak
	for i := range samples {
		samples[i] *= scale
	}
}

func fade(samples []float64, fadeLen int) {
	if fadeLen <= 0 || len(samples) == 0 {
		return
	}
	if fadeLen*2 > len(samples) {
		fadeLen = len(samples) / 2
	}
	for i := 0; i < fadeLen; i++ {
		gain := float64(i) / float64(fadeLen)
		samples[i] *= gain
		samples[len(samples)-1-i] *= gain
	}
}

func hamming(n int) []float64 {
	w := make([]float64, n)
	if n <= 1 {
		if n == 1 {
			w[0] = 1
		}
		return w
	}
	for i := range w {
		w[i] = 0.54 - 0.46*math.Cos(twoPi*float64(i)/float64(n-1))
	}
	return w
}

func fft(a []complex128) {
	n := len(a)
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j &^= bit
		}
		j |= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		angle := -twoPi / float64(length)
		wlen := complex(math.Cos(angle), math.Sin(angle))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for k := 0; k < length/2; k++ {
				u := a[i+k]
				v := a[i+k+length/2] * w
				a[i+k] = u + v
				a[i+k+length/2] = u - v
				w *= wlen
			}
		}
	}
}

func cmplxAbs(c complex128) float64 {
	return math.Hypot(real(c), imag(c))
}

func palette(v float64) color.RGBA {
	v = clampFloat(v, 0, 1)
	colors := []color.RGBA{
		{R: 68, G: 1, B: 84, A: 255},
		{R: 72, G: 40, B: 120, A: 255},
		{R: 62, G: 73, B: 137, A: 255},
		{R: 49, G: 104, B: 142, A: 255},
		{R: 38, G: 130, B: 142, A: 255},
		{R: 31, G: 158, B: 137, A: 255},
		{R: 53, G: 183, B: 121, A: 255},
		{R: 110, G: 206, B: 88, A: 255},
		{R: 181, G: 222, B: 43, A: 255},
		{R: 253, G: 231, B: 37, A: 255},
	}
	scaled := v * float64(len(colors)-1)
	idx := int(math.Floor(scaled))
	if idx >= len(colors)-1 {
		return colors[len(colors)-1]
	}
	t := scaled - float64(idx)
	a, b := colors[idx], colors[idx+1]
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

func nearestPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	prev := p >> 1
	if p-n < n-prev {
		return p
	}
	return prev
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
