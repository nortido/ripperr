package logger

import (
	"fmt"
	"time"
)

func Debug(msg string) {
	fmt.Println(time.Now().Format(time.DateTime), "DEBUG: ", msg)
}

func Info(msg string) {
	fmt.Println(time.Now().Format(time.DateTime), "INFO: ", msg)
}

func Warn(msg string) {
	fmt.Println(time.Now().Format(time.DateTime), "WARN: ", msg)
}

func Error(msg string) {
	fmt.Println(time.Now().Format(time.DateTime), "ERROR: ", msg)
}
