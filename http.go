package main

import (
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// RegexpServeMux is an HTTP servemux which matches incoming request URLs against a list
// of registered regexps, extracts the named match groups as path values, and then calls
// the handler for the first pattern that matches the URL.
// TODO: doesn't redirect paths without trailing slashes to paths with trailing slashes
type RegexpServeMux struct {
	routes []regexpServeRoute
}

type regexpServeRoute struct {
	path    *regexp.Regexp
	handler http.Handler
}

func NewRegexpServeMux() RegexpServeMux {
	return RegexpServeMux{}
}

func (router *RegexpServeMux) Handle(path string, handler http.Handler) {
	router.routes = append(router.routes, regexpServeRoute{regexp.MustCompile(path), handler})
}

func (router *RegexpServeMux) HandleFunc(path string, handler http.HandlerFunc) {
	router.Handle(path, handler)
}

func (router RegexpServeMux) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	for _, route := range router.routes {
		match := route.path.FindStringSubmatch(req.URL.Path)
		if match != nil {
			for i, name := range route.path.SubexpNames() {
				if name != "" {
					req.SetPathValue(name, match[i])
				}
			}
			route.handler.ServeHTTP(w, req)
			return
		}
	}
	http.NotFound(w, req)
}

// LoggingMiddleware returns an http.Handler which logs responses to incoming
// HTTP requests.
func LoggingMiddleware(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		loggingWriter := &loggingResponseWriter{w, 200}
		startTime := time.Now()
		inner.ServeHTTP(loggingWriter, req)
		responseDuration := time.Now().Sub(startTime)
		log.Printf("%d %s - %s %s - (%v)\n",
			loggingWriter.StatusCode, http.StatusText(loggingWriter.StatusCode),
			req.Method, req.URL.String(), responseDuration,
		)
	})
}

type loggingResponseWriter struct {
	wrapped    http.ResponseWriter
	StatusCode int
}

func (writer *loggingResponseWriter) Header() http.Header {
	return writer.wrapped.Header()
}

func (writer *loggingResponseWriter) Write(data []byte) (int, error) {
	return writer.wrapped.Write(data)
}

func (writer *loggingResponseWriter) WriteHeader(statusCode int) {
	writer.StatusCode = statusCode
	// TODO: handle the "superfluous call to WriteHeader" messages
	writer.wrapped.WriteHeader(statusCode)
}

// OneAtATimeMiddleware returns an http.Handler which ensures only one HTTP
// request is handled at a time.
func OneAtATimeMiddleware(inner http.Handler) http.Handler {
	var oneAtATime sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		oneAtATime.Lock()
		defer oneAtATime.Unlock()
		inner.ServeHTTP(w, req)
	})
}
