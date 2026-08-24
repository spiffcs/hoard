package ui

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

const probeWait = 150 * time.Millisecond

func ProbeCellAspect() float64 {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0
	}
	defer tty.Close()

	fd := tty.Fd()
	if !term.IsTerminal(fd) {
		return 0
	}
	if err := tty.SetReadDeadline(time.Now().Add(probeWait)); err != nil {
		return 0
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return 0
	}
	defer term.Restore(fd, state)

	if _, err := tty.WriteString("\x1b[16t\x1b[14t"); err != nil {
		return 0
	}
	buf := make([]byte, 64)
	n, err := tty.Read(buf)
	if n <= 0 || (err != nil && n == 0) {
		return 0
	}
	reply := buf[:n]

	cellH, cellW, _ := parseSizeReport(reply, '6')
	winH, winW, _ := parseSizeReport(reply, '4')
	cols, rows, _ := term.GetSize(fd)
	return cellAspectFrom(cellH, cellW, winH, winW, rows, cols)
}

func parseSizeReport(b []byte, kind byte) (h, w int, ok bool) {
	for i := 0; i+3 < len(b); i++ {
		if b[i] != 0x1b || b[i+1] != '[' || b[i+2] != kind || b[i+3] != ';' {
			continue
		}
		end := -1
		for j := i + 4; j < len(b); j++ {
			if b[j] == 't' {
				end = j
				break
			}
			if b[j] != ';' && (b[j] < '0' || b[j] > '9') {
				break
			}
		}
		if end < 0 {
			continue
		}
		parts := strings.Split(string(b[i+4:end]), ";")
		if len(parts) != 2 {
			continue
		}
		hh, herr := strconv.Atoi(parts[0])
		ww, werr := strconv.Atoi(parts[1])
		if herr != nil || werr != nil || hh <= 0 || ww <= 0 {
			continue
		}
		return hh, ww, true
	}
	return 0, 0, false
}

func cellAspectFrom(cellH, cellW, winH, winW, rows, cols int) float64 {
	if a := plausibleAspect(ratio(cellH, cellW)); a > 0 {
		return a
	}
	if rows > 0 && cols > 0 && winH > 0 && winW > 0 {
		return plausibleAspect((float64(winH) / float64(rows)) / (float64(winW) / float64(cols)))
	}
	return 0
}

func ratio(h, w int) float64 {
	if h <= 0 || w <= 0 {
		return 0
	}
	return float64(h) / float64(w)
}

func plausibleAspect(a float64) float64 {
	if a < 1 || a > 4 {
		return 0
	}
	return a
}
