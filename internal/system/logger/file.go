package logger

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const fileName = "ripperr.log"

var (
	maxFileSize int64 = 5 << 20

	// Один мьютекс на stdout и файл: порядок строк в них одинаковый.
	fileMu         sync.Mutex
	logFile        *os.File
	logSize        int64
	logDir         string
	writeReported  bool
	rotateReported bool
)

func emit(label, msg string, buffered bool) {
	now := time.Now()
	line := now.Format(time.DateTime) + " " + label + " " + msg + "\n"

	fileMu.Lock()
	_, _ = os.Stdout.WriteString(line)
	reports := writeFileLocked(line)
	if buffered {
		globalBuffer.add(Entry{Time: now, Level: strings.TrimSuffix(label, ":"), Message: msg})
	}
	fileMu.Unlock()

	for _, r := range reports {
		report(r)
	}
}

func report(msg string) {
	// Вызывается после Unlock и пишет мимо файла, иначе сбой файла зациклился бы на себе
	now := time.Now()
	text := "logger: file log: " + msg
	fileMu.Lock()
	_, _ = os.Stdout.WriteString(now.Format(time.DateTime) + " WARN: " + text + "\n")
	fileMu.Unlock()
	globalBuffer.add(Entry{Time: now, Level: "WARN", Message: text})
}

func writeFileLocked(line string) []string {
	if logFile == nil {
		return nil
	}
	var reports []string
	if logSize > 0 && logSize+int64(len(line)) > maxFileSize {
		reports = rotateLocked()
		if logFile == nil {
			return reports
		}
	}
	n, err := logFile.WriteString(line)
	logSize += int64(n)
	if err != nil {
		if !writeReported {
			writeReported = true
			reports = append(reports, "write: "+err.Error())
		}
		return reports
	}
	writeReported = false
	return reports
}

func rotateLocked() []string {
	_ = logFile.Close()
	logFile = nil
	base := filepath.Join(logDir, fileName)

	var reports []string
	renames := [][2]string{{base + ".2", base + ".3"}, {base + ".1", base + ".2"}, {base, base + ".1"}}
	for _, r := range renames {
		if err := os.Rename(r[0], r[1]); err != nil && !os.IsNotExist(err) {
			if !rotateReported {
				rotateReported = true
				reports = append(reports, "rotate: "+err.Error())
			}
			break
		}
		if r[0] == base {
			rotateReported = false
		}
	}

	f, err := os.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return append(reports, "reopen: "+err.Error())
	}
	logFile = f
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		reports = append(reports, "crash output: "+err.Error())
	}
	// После неудачной ротации следующая попытка только через новые maxFileSize байт.
	logSize = 0
	return reports
}

func InitFile(dir string) {
	fileMu.Lock()
	initialized := logFile != nil
	fileMu.Unlock()
	if initialized {
		return
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		report("mkdir: " + err.Error())
		return
	}

	entries, reports := readTail(dir)
	for _, e := range entries {
		globalBuffer.add(e)
	}
	for _, r := range reports {
		report(r)
	}

	f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		report("open: " + err.Error())
		return
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		report("stat: " + err.Error())
		return
	}

	fileMu.Lock()
	logFile, logSize, logDir = f, st.Size(), dir
	fileMu.Unlock()

	// SetCrashOutput дублирует дескриптор, поэтому после ротации вызывается снова
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		report("crash output: " + err.Error())
	}
}
