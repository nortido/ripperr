package logger

import "strings"

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var minLevel = LevelInfo

func SetLevel(level string) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		minLevel = LevelDebug
	case "info":
		minLevel = LevelInfo
	case "warn", "warning":
		minLevel = LevelWarn
	case "error":
		minLevel = LevelError
	default:
		minLevel = LevelInfo
	}
}

func log(level Level, label, msg string) {
	if level < minLevel {
		return
	}
	emit(label, msg, true)
}

func Debug(msg string) {
	if minLevel > LevelDebug {
		return
	}
	emit("DEBUG:", msg, false)
}

func Info(msg string) {
	log(LevelInfo, "INFO:", msg)
}

func Warn(msg string) {
	log(LevelWarn, "WARN:", msg)
}

func Error(msg string) {
	log(LevelError, "ERROR:", msg)
}
