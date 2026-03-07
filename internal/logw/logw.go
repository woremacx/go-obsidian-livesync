package logw

import (
	"fmt"
	"log"
	"os"
)

type LogWrapType int

const (
	LogWrapInfo LogWrapType = iota
	LogWrapDebug
	LogWrapTrace
)

const debugPrefix = "[DEBUG] "
const tracePrefix = "[TRACE] "
const warnPrefix = "[WARN] "
const errorPrefix = "[ERROR] "
const fatalPrefix = "[FATAL] "
const infoPrefix = "[INFO] "

var logLevel = LogWrapInfo

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

func SetLevel(level LogWrapType) {
	logLevel = level
}

func Infof(format string, v ...interface{}) {
	log.Output(3, infoPrefix+fmt.Sprintf(format, v...))
}

func Infoln(v ...interface{}) {
	log.Output(3, infoPrefix+fmt.Sprintln(v...))
}

func Debugf(format string, v ...interface{}) {
	if logLevel < LogWrapDebug {
		return
	}
	log.Output(3, debugPrefix+fmt.Sprintf(format, v...))
}

func Debugln(v ...interface{}) {
	if logLevel < LogWrapDebug {
		return
	}
	log.Output(3, debugPrefix+fmt.Sprintln(v...))
}

func Tracef(format string, v ...interface{}) {
	if logLevel < LogWrapTrace {
		return
	}
	log.Output(3, tracePrefix+fmt.Sprintf(format, v...))
}

func Traceln(v ...interface{}) {
	if logLevel < LogWrapTrace {
		return
	}
	log.Output(3, tracePrefix+fmt.Sprintln(v...))
}

func Warnf(format string, v ...interface{}) {
	log.Output(3, warnPrefix+fmt.Sprintf(format, v...))
}

func Warnln(v ...interface{}) {
	log.Output(3, warnPrefix+fmt.Sprintln(v...))
}

func Errorf(format string, v ...interface{}) {
	log.Output(3, errorPrefix+fmt.Sprintf(format, v...))
}

func Errorln(v ...interface{}) {
	log.Output(3, errorPrefix+fmt.Sprintln(v...))
}

func Fatalf(format string, v ...interface{}) {
	log.Output(3, fatalPrefix+fmt.Sprintf(format, v...))
	os.Exit(1)
}

func Fatalln(v ...interface{}) {
	log.Output(3, fatalPrefix+fmt.Sprintln(v...))
	os.Exit(1)
}
