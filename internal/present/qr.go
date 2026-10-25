package present

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
)

// WantQR reports whether a terminal QR should be drawn.
func WantQR() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// QR renders text as a scannable block using an explicit white background.
func QR(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	code, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return "", err
	}
	img := code.Image(1)
	return render(img), nil
}

// FprintQR writes a QR code to w.
func FprintQR(w io.Writer, text string) error {
	s, err := QR(text)
	if err != nil || s == "" {
		return err
	}
	_, err = io.WriteString(w, s)
	return err
}

func render(img image.Image) string {
	b := img.Bounds()
	var out strings.Builder
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		out.WriteString("\033[48;5;255m\033[38;5;0m")
		for x := b.Min.X; x < b.Max.X; x++ {
			top := dark(img.At(x, y))
			bot := false
			if y+1 < b.Max.Y {
				bot = dark(img.At(x, y+1))
			}
			switch {
			case top && bot:
				out.WriteRune('█')
			case top && !bot:
				out.WriteRune('▀')
			case !top && bot:
				out.WriteRune('▄')
			default:
				out.WriteRune(' ')
			}
		}
		out.WriteString("\033[0m\n")
	}
	return out.String()
}

func dark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r+g+b < 0x18000
}

// ShareLines is the human hint printed with a public or LAN URL.
func ShareLines(url, hint, errText string) string {
	var b strings.Builder
	if url != "" {
		fmt.Fprintf(&b, "share: %s\n", url)
	}
	if hint != "" {
		b.WriteString(hint)
		b.WriteByte('\n')
	}
	if errText != "" {
		fmt.Fprintf(&b, "share error: %s\n", errText)
	}
	return b.String()
}
