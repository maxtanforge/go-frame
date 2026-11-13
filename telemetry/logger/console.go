package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

func validateFormat(format string) error {
	switch strings.ToLower(format) {
	case "", "console", "json", "sls":
		return nil
	default:
		return fmt.Errorf("invalid log format %q", format)
	}
}

func outputHandler(o Options, w io.Writer, level slog.Leveler) slog.Handler {
	location, _ := logLocation(o.Timezone) // New falls back; Open validates first.
	if strings.EqualFold(o.Format, "json") {
		h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, AddSource: o.AddSource})
		return timezoneHandler{Handler: h, location: location}
	}
	state := &consoleState{writer: w}
	return consoleHandler{
		state: state, level: level, source: o.AddSource, location: location,
		sls: strings.EqualFold(o.Format, "sls"), environment: o.Environment,
		attrs: slog.NewJSONHandler(&state.buffer, nil),
	}
}

// JSONHandler resolves and encodes attributes with standard slog semantics.
// Console presentation preserves field order, groups and numeric precision.
// Children share scratch storage and serialize each complete record write.
type consoleState struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	writer io.Writer
}

type consoleHandler struct {
	state       *consoleState
	attrs       slog.Handler
	level       slog.Leveler
	source      bool
	location    *time.Location
	sls         bool
	environment string
}

func (h consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h consoleHandler) Handle(ctx context.Context, record slog.Record) error {
	// WithAttrs resolves and serializes application values before the output
	// lock: LogValuer and MarshalJSON may both write another log recursively.
	fieldsToEncode := make([]slog.Attr, 0, record.NumAttrs()+1)
	if h.source && record.PC != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
		fieldsToEncode = append(fieldsToEncode, slog.String("source", fmt.Sprintf("%s:%d", frame.File, frame.Line)))
	}
	record.Attrs(func(a slog.Attr) bool { fieldsToEncode = append(fieldsToEncode, a); return true })
	encoded := h.attrs.WithAttrs(fieldsToEncode)
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.buffer.Reset()
	// The fixed synthetic header is removed before formatting attributes. This
	// keeps user fields named time, level or msg, including inherited fields.
	attrs := slog.NewRecord(time.Time{}, slog.LevelInfo, "", 0)
	if err := encoded.Handle(ctx, attrs); err != nil {
		return err
	}
	data := bytes.TrimSuffix(bytes.TrimPrefix(h.state.buffer.Bytes(), []byte(`{"level":"INFO","msg":""`)), []byte("\n"))
	data = append([]byte{'{'}, bytes.TrimPrefix(data, []byte{','})...)
	fields, stacks, err := consoleFields(data)
	if err != nil {
		return err
	}
	message := record.Message
	if strings.IndexFunc(message, func(r rune) bool { return !strconv.IsPrint(r) }) >= 0 {
		message = strconv.Quote(message)
	}
	timestamp := record.Time
	if h.location != nil {
		timestamp = timestamp.In(h.location)
	}
	line := fmt.Sprintf("[%s] [%s] %s", timestamp.Format("2006-01-02 15:04:05.000"), strings.ToLower(record.Level.String()), message)
	if fields != "{}" {
		line += " " + fields
	}
	line += "\n"
	for _, stack := range stacks {
		// Stack blocks are readable continuation lines of this record, and are
		// written atomically with its header (including during file rotation).
		stack = strings.ReplaceAll(strings.ReplaceAll(stack, "\r\n", "\n"), "\r", `\r`)
		stack = escapeConsoleStack(stack)
		line += "  " + strings.ReplaceAll(strings.TrimRight(stack, "\n"), "\n", "\n  ") + "\n"
	}
	if h.sls {
		environment := h.environment
		if environment == "" {
			environment = "production"
		}
		content, err := json.Marshal(slsContent{
			Message: strings.TrimSuffix(line, "\n"), Env: environment,
			Level: record.Level.String(), PID: strconv.Itoa(os.Getpid()),
		})
		if err != nil {
			return err
		}
		line = string(content) + "\n"
	}
	n, err := io.WriteString(h.state.writer, line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	return err
}

// Stack blocks retain line breaks and indentation; other non-printing characters
// must not reach a terminal or collector as active control sequences.
func escapeConsoleStack(stack string) string {
	var output strings.Builder
	for _, r := range stack {
		if r == '\n' || r == '\t' || strconv.IsPrint(r) {
			output.WriteRune(r)
		} else {
			quoted := strconv.QuoteRune(r)
			output.WriteString(quoted[1 : len(quoted)-1])
		}
	}
	return output.String()
}

func (h consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = h.attrs.WithAttrs(attrs)
	return h
}

func (h consoleHandler) WithGroup(name string) slog.Handler {
	h.attrs = h.attrs.WithGroup(name)
	return h
}

// consoleFields renders the ordered JSON attribute object as an inspect-style
// object. Only a root string stack is promoted to a continuation block.
func consoleFields(data []byte) (string, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return "", nil, err
	}
	var fields []string
	var stacks []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		value, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		if stack, ok := value.(string); key == "stack" && ok {
			if stack != "" {
				stacks = append(stacks, stack)
			}
			continue
		}
		formatted, err := consoleValue(dec, value)
		if err != nil {
			return "", nil, err
		}
		fields = append(fields, consoleKey(key.(string))+": "+formatted)
	}
	if _, err := dec.Token(); err != nil {
		return "", nil, err
	}
	return consoleContainer(fields, true), stacks, nil
}

func consoleValue(dec *json.Decoder, token any) (string, error) {
	switch value := token.(type) {
	case json.Delim:
		var items []string
		for dec.More() {
			first, err := dec.Token()
			if err != nil {
				return "", err
			}
			key := ""
			if value == '{' {
				key = consoleKey(first.(string)) + ": "
				first, err = dec.Token()
				if err != nil {
					return "", err
				}
			}
			item, err := consoleValue(dec, first)
			if err != nil {
				return "", err
			}
			items = append(items, key+item)
		}
		if _, err := dec.Token(); err != nil {
			return "", err
		}
		return consoleContainer(items, value == '{'), nil
	case string:
		return consoleQuote(value), nil
	case nil:
		return "null", nil
	default:
		return fmt.Sprint(value), nil
	}
}

// Metadata stays on one line regardless of size. Scalar newlines are escaped.
func consoleContainer(items []string, object bool) string {
	opening, closing, padding := "[", "]", ""
	if object {
		opening, closing, padding = "{", "}", " "
	}
	if len(items) == 0 {
		return opening + closing
	}
	return opening + padding + strings.Join(items, ", ") + padding + closing
}

func consoleQuote(value string) string {
	quoted := strconv.Quote(value)
	quoted = strings.ReplaceAll(quoted[1:len(quoted)-1], `\"`, `"`)
	return "'" + strings.ReplaceAll(quoted, "'", `\'`) + "'"
}

func consoleKey(key string) string {
	if key == "" {
		return consoleQuote(key)
	}
	for i, r := range key {
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return consoleQuote(key)
	}
	return key
}
