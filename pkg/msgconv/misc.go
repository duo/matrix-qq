package msgconv

import (
	"bytes"
	"os"
	"os/exec"

	"github.com/pinwang5776/silk"
)

func silk2ogg(rawData []byte) ([]byte, error) {
	buf := bytes.NewBuffer(rawData)
	pcmData, err := silk.Decode(buf)
	if err != nil {
		return nil, err
	}

	pcmFile, err := os.CreateTemp("", "pcm-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(pcmFile.Name())
	os.WriteFile(pcmFile.Name(), pcmData, 0o644)

	wavFile, err := os.CreateTemp("", "wav-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(wavFile.Name())
	{
		cmd := exec.Command(
			"ffmpeg", "-f", "s16le", "-ar", "24000", "-ac", "1", "-y", "-i", pcmFile.Name(), "-f", "wav", "-af", "volume=7.812500", wavFile.Name())
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		if err := cmd.Wait(); err != nil {
			return nil, err
		}
	}

	oggFile, err := os.CreateTemp("", "ogg-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(oggFile.Name())
	{
		cmd := exec.Command(
			"ffmpeg", "-y", "-i", wavFile.Name(), "-c:a", "libopus", "-b:a", "24K", "-f", "ogg", oggFile.Name())
		if err := cmd.Start(); err != nil {
			return nil, err
		}

		if err := cmd.Wait(); err != nil {
			return nil, err
		}
	}

	return os.ReadFile(oggFile.Name())
}

func ogg2silk(rawData []byte) ([]byte, error) {
	oggFile, err := os.CreateTemp("", "ogg-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(oggFile.Name())
	os.WriteFile(oggFile.Name(), rawData, 0o644)

	wavFile, err := os.CreateTemp("", "wav-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(wavFile.Name())
	{
		cmd := exec.Command(
			"ffmpeg", "-y", "-i", oggFile.Name(), "-f", "s16le", "-ar", "24000", "-ac", "1", wavFile.Name())
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		if err := cmd.Wait(); err != nil {
			return nil, err
		}
	}

	wavData, err := os.Open(wavFile.Name())
	if err != nil {
		return nil, err
	}

	silkData, err := silk.Encode(wavData, silk.Stx(true))
	if err != nil {
		return nil, err
	}

	return silkData, nil
}
