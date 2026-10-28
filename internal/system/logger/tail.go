package logger

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var levelLabels = []string{"DEBUG:", "INFO:", "WARN:", "ERROR:"}

func parseHeader(line string) (time.Time, string, string, bool) {
	if len(line) < len(time.DateTime)+1 || line[len(time.DateTime)] != ' ' {
		return time.Time{}, "", "", false
	}
	t, err := time.ParseInLocation(time.DateTime, line[:len(time.DateTime)], time.Local)
	if err != nil {
		return time.Time{}, "", "", false
	}
	rest := line[len(time.DateTime)+1:]
	for _, label := range levelLabels {
		after, ok := strings.CutPrefix(rest, label)
		if !ok {
			continue
		}
		if after == "" {
			return t, strings.TrimSuffix(label, ":"), "", true
		}
		if after[0] == ' ' {
			return t, strings.TrimSuffix(label, ":"), after[1:], true
		}
	}
	return time.Time{}, "", "", false
}

func parseTail(data string) []Entry {
	data = strings.TrimSuffix(data, "\n")
	if data == "" {
		return nil
	}
	var entries []Entry
	var cur Entry
	have, skip := false, false
	flush := func() {
		if have && !skip {
			entries = append(entries, cur)
		}
	}
	for _, line := range strings.Split(data, "\n") {
		if t, level, msg, ok := parseHeader(line); ok {
			flush()
			cur = Entry{Time: t, Level: level, Message: msg}
			have, skip = true, level == "DEBUG"
			continue
		}
		if have {
			cur.Message += "\n" + line
		}
	}
	flush()
	return entries
}

func readTail(dir string) ([]Entry, []string) {
	var reports []string
	read := func(name string) []Entry {
		b, err := readWindow(filepath.Join(dir, name), maxFileSize)
		if err != nil {
			if !os.IsNotExist(err) && len(reports) == 0 {
				reports = append(reports, "preload: "+err.Error())
			}
			return nil
		}
		return parseTail(b)
	}

	entries := read(fileName)
	if len(entries) < defaultBufferCap {
		entries = append(read(fileName+".1"), entries...)
	}
	if len(entries) > defaultBufferCap {
		entries = entries[len(entries)-defaultBufferCap:]
	}
	// копируем, иначе сообщения держат в памяти весь прочитанный кусок файла
	for i := range entries {
		entries[i].Message = strings.Clone(entries[i].Message)
	}
	return entries, reports
}

func readWindow(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	cut := st.Size() > limit
	if cut {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	s := string(b)
	if cut {
		// Первая строка окна обрезана, ее начало осталось за границей
		_, s, _ = strings.Cut(s, "\n")
	}
	return s, nil
}
