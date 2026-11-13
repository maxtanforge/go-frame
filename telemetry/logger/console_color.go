package logger

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// Color belongs to the terminal sink, so simultaneous file output stays plain.
func consoleOutput(file *os.File, format string) io.Writer {
	_, noColor := os.LookupEnv("NO_COLOR")
	if strings.EqualFold(format, "json") || strings.EqualFold(format, "sls") || noColor || os.Getenv("TERM") == "dumb" {
		return file
	}
	if isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd()) {
		return consoleColorWriter{writer: file}
	}
	return file
}

type consoleColorWriter struct{ writer io.Writer }

func (w consoleColorWriter) Write(p []byte) (int, error) {
	start := bytes.Index(p, []byte("] ["))
	if start < 0 {
		return w.writer.Write(p)
	}
	start += 2 // Keep the timestamp plain; color the level and message.
	end := bytes.IndexByte(p[start:], ']')
	if end < 0 {
		return w.writer.Write(p)
	}
	color := "\x1b[32m"
	switch string(p[start+1 : start+end]) {
	case "error":
		color = "\x1b[31m"
	case "warn":
		color = "\x1b[33m"
	case "debug":
		color = "\x1b[34m"
	}
	var output bytes.Buffer
	output.Write(p[:start])
	output.WriteString(color)
	output.Write(bytes.TrimSuffix(p[start:], []byte("\n")))
	output.WriteString("\x1b[0m\n")
	n, err := w.writer.Write(output.Bytes())
	if err != nil {
		return 0, err
	}
	if n != output.Len() {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
