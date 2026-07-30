package utils

import (
	"log"
	"log/syslog"
	"os"
	"runtime"
)

var (
	errorLogger   *log.Logger
	warningLogger *log.Logger
	infoLogger    *log.Logger
)

func init() {
	// Fall back to stderr when the log file is not writable (e.g. running
	// tests or a dev build outside the container) instead of refusing to start.
	var out = os.Stderr
	logFile, err := os.OpenFile("/var/log/loxioam.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file, logging to stderr: %s", err)
	} else {
		out = logFile
	}

	errorLogger = log.New(out, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	warningLogger = log.New(out, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	infoLogger = log.New(out, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
}

// LogToSyslog logs a message to syslog with the specified priority and function name.
func LogToSyslog(priority syslog.Priority, message string) {
	pc, _, _, ok := runtime.Caller(1)
	funcName := "unknown"
	if ok {
		funcName = runtime.FuncForPC(pc).Name()
	}

	logger, err := syslog.New(priority, "loxilb-oam")
	if err != nil {
		errorLogger.Printf("Failed to connect to syslog: %s", err)
		return
	}
	defer logger.Close()

	logger.Write([]byte(funcName + ": " + message))
}

func LogError(message string) {
	pc, _, _, ok := runtime.Caller(1)
	funcName := "unknown"
	if ok {
		funcName = runtime.FuncForPC(pc).Name()
	}
	errorLogger.Println(funcName + ": " + message)
}

func LogWarning(message string) {
	pc, _, _, ok := runtime.Caller(1)
	funcName := "unknown"
	if ok {
		funcName = runtime.FuncForPC(pc).Name()
	}
	warningLogger.Println(funcName + ": " + message)
}

func LogInfo(message string) {
	pc, _, _, ok := runtime.Caller(1)
	funcName := "unknown"
	if ok {
		funcName = runtime.FuncForPC(pc).Name()
	}
	infoLogger.Println(funcName + ": " + message)
}
