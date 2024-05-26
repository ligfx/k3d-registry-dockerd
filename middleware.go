package main

import (
	"log"
	"net/http"
	"sync"
	"time"
)

type LoggingResponseWriter struct {
	wrapped    http.ResponseWriter
	StatusCode int
}

func (writer *LoggingResponseWriter) Header() http.Header {
	return writer.wrapped.Header()
}

func (writer *LoggingResponseWriter) Write(data []byte) (int, error) {
	return writer.wrapped.Write(data)
}

func (writer *LoggingResponseWriter) WriteHeader(statusCode int) {
	writer.StatusCode = statusCode
	// TODO: handle the "superfluous call to WriteHeader" messages
	writer.wrapped.WriteHeader(statusCode)
}

func LoggingMiddleware(inner http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// headers := ""
		// for key, values := range req.Header {
		// 	if key == "User-Agent" || key == "Accept-Encoding" {
		// 		continue
		// 	}
		// 	headers = fmt.Sprintf("%s %s=%v", headers, key, values)
		// }
		// log.Printf("> %s %s%s", req.Method, req.URL.String(), headers)
		loggingWriter := &LoggingResponseWriter{w, 200}
		startTime := time.Now()
		inner.ServeHTTP(loggingWriter, req)
		responseDuration := time.Now().Sub(startTime)

		// headers = ""
		// for key, values := range w.Header() {
		// 	headers = fmt.Sprintf("%s %s=%v", headers, key, values)
		// }

		log.Printf("%d %s - %s %s - (%v)\n",
			loggingWriter.StatusCode, http.StatusText(loggingWriter.StatusCode),
			req.Method, req.URL.String(), responseDuration,
		)
	}
}

func OneAtATimeMiddleware(inner http.HandlerFunc) http.HandlerFunc {
	var oneAtATime sync.Mutex
	return func(w http.ResponseWriter, req *http.Request) {
		oneAtATime.Lock()
		defer oneAtATime.Unlock()
		inner.ServeHTTP(w, req)
	}
}
