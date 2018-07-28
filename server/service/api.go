package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	httpGET    = "GET"
	httpPOST   = "POST"
	httpPUT    = "PUT"
	httpDELETE = "DELETE"
)

func runAPI(theDB *sql.DB, port int, devmode bool) {
	mux := http.NewServeMux()
	mux.Handle("/api/v0/awaitingApproval", &apiMethodAwaitingApproval{db: theDB})
	mux.Handle("/api/v0/awaitingApproval/", &apiMethodAwaitingApproval{db: theDB})
	mux.Handle("/api/v0/file", &apiMethodFile{db: theDB})
	mux.Handle("/api/v0/host", &apiMethodHost{db: theDB})
	mux.Handle("/api/v0/hostlist", &apiMethodHostList{db: theDB, devmode: devmode})
	mux.Handle("/api/v0/searchpage", &apiMethodSearchPage{db: theDB, devmode: devmode})
	mux.Handle("/api/v0/settings/ipranges", &apiMethodIpRanges{db: theDB})
	mux.Handle("/api/v0/settings/ipranges/", &apiMethodIpRanges{db: theDB})
	mux.Handle("/api/v0/settings/", &apiMethodSettings{db: theDB})
	mux.Handle("/api/v0/settings", &apiMethodSettings{db: theDB})
	mux.Handle("/api/v0/settings/customfields", &apiMethodCustomFieldsCollection{db: theDB})
	mux.Handle("/api/v0/settings/customfields/", &apiMethodCustomFieldsItem{db: theDB})
	mux.Handle("/api/v0/status", &apiMethodStatus{db: theDB})
	mux.HandleFunc("/api/internal/triggerJob/", runJob)
	mux.HandleFunc("/api/internal/unsetCurrent", unsetCurrent)
	mux.HandleFunc("/api/internal/countFiles", countFiles)
	mux.HandleFunc("/api/internal/mu", doNothing)
	var h http.Handler = mux
	if devmode {
		h = wrapLog(wrapAllowLocalhostCORS(h))
	}
	log.Printf("Serving API requests on localhost:%d\n", port)
	err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), h)
	if err != nil {
		log.Println(err.Error())
	}
}

// returnJSON marshals the given object and writes it as the response,
// and also sets the proper Content-Type header.
// Remember to return after calling this function.
func returnJSON(w http.ResponseWriter, req *http.Request, data interface{}) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Println(err.Error())
		return
	}
	bytes = append(bytes, 0xA) // end with a line feed, because I'm a nice person
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(bytes)
}

// For requests originating from localhost (typically on another port),
// this wrapper adds http headers that allow that origin.
// This makes it easier to test locally when developing.
func wrapAllowLocalhostCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		match, err := regexp.MatchString("http://(127\\.0\\.0\\.1|localhost)",
			req.Header.Get("Origin"))
		if match {
			w.Header().Set("Access-Control-Allow-Origin", req.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods",
				"GET, POST, HEAD, OPTIONS, PUT, DELETE, PATCH")
			w.Header().Set("Vary", "Origin")
		} else if err != nil {
			log.Println(err)
		}
		if req.Method == "OPTIONS" {
			// When cross-domain, browsers sends OPTIONS first, to check for CORS headers
			// See: https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
			http.Error(w, "", http.StatusNoContent) // 204 OK
			return
		}
		h.ServeHTTP(w, req)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func wrapLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		lrw := &loggingResponseWriter{w, http.StatusOK}
		h.ServeHTTP(lrw, req)
		log.Printf("[%d] %s %s\n", lrw.statusCode, req.Method, req.URL)
	})
}

// Wrappers for sql nulltypes that encodes the values when marshalling JSON
type jsonTime pq.NullTime
type jsonString sql.NullString

func (jst jsonTime) MarshalJSON() ([]byte, error) {
	if jst.Valid && !jst.Time.IsZero() {
		return []byte(fmt.Sprintf("\"%s\"", jst.Time.Format(time.RFC3339))), nil
	}
	return []byte("null"), nil
}

func (ns jsonString) MarshalJSON() ([]byte, error) {
	if ns.Valid {
		return json.Marshal(ns.String)
	}
	return []byte("null"), nil
}

type httpError struct {
	message string
	code    int
}

func unpackFieldParam(fieldParam string, allowedFields []string) (map[string]bool, *httpError) {
	if fieldParam == "" {
		return nil, &httpError{
			message: "Missing or empty parameter: fields",
			code:    http.StatusUnprocessableEntity,
		}
	}
	fields := make(map[string]bool)
	for _, f := range strings.Split(fieldParam, ",") {
		ok := false
		for _, af := range allowedFields {
			if strings.EqualFold(f, af) {
				ok = true
				fields[af] = true
				break
			}
		}
		if !ok {
			return nil, &httpError{
				message: "Unsupported field name: " + f,
				code:    http.StatusUnprocessableEntity,
			}
		}
	}
	return fields, nil
}

func contains(needle string, haystack []string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func runJob(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(req.RemoteAddr, "127.0.0.1:") {
		http.Error(w, "", http.StatusForbidden)
		return
	}
	match := regexp.MustCompile("/(\\w+)$").FindStringSubmatch(req.URL.Path)
	if match == nil {
		http.Error(w, "Missing job name in URL path", http.StatusUnprocessableEntity)
		return
	}
	for i, jobitem := range jobs {
		t := reflect.TypeOf(jobitem.job)
		if t.Name() == match[1] {
			// this will make main run the job
			jobs[i].trigger = true
			http.Error(w, "OK", http.StatusNoContent)
			return
		}
	}
	http.Error(w, "Job not found.", http.StatusNotFound)
}

func unsetCurrent(w http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.RemoteAddr, "127.0.0.1:") {
		http.Error(w, "", http.StatusForbidden)
		return
	}
	for _, s := range strings.Split(req.FormValue("ids"), ",") {
		fileID, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			removeFileFromFastSearch(fileID)
		}
	}
	http.Error(w, "OK", http.StatusNoContent)
}

func countFiles(w http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.RemoteAddr, "127.0.0.1:") {
		http.Error(w, "", http.StatusForbidden)
		return
	}
	i, err := strconv.Atoi(req.FormValue("n"))
	if err != nil || i == 0 {
		return
	}
	pfib.Add(float64(i))
}

func doNothing(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "無\n") // https://en.wikipedia.org/wiki/Mu_(negative)
}

func isTrueish(s string) bool {
	s = strings.ToLower(s)
	return s == "1" || s == "t" || s == "true" || s == "yes" || s == "y"
}

// QueryList performs a database query and returns a slice of maps.
// For convenience, the slice can be passed directly to returnJSON.
func QueryList(db *sql.DB, statement string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := db.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		// Source: https://kylewbanks.com/blog/query-result-to-map-in-golang

		// Create a slice of interface{}'s to represent each column,
		// and a second slice to contain pointers to each item in the columns slice.
		columns := make([]interface{}, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			return nil, err
		}

		// Create our map, and retrieve the value for each column from the pointers slice,
		// storing it in the map with the name of the column as the key.
		m := make(map[string]interface{})
		for i, colName := range cols {
			val := columnPointers[i].(*interface{})
			m[colName] = *val
		}

		result = append(result, m)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return result, nil
}

// QueryColumn performs a database query that is expected to return one column,
// and returns a slice with the values.
// For convenience, the slice can be passed directly to returnJSON.
func QueryColumn(db *sql.DB, statement string, args ...interface{}) ([]interface{}, error) {
	rows, err := db.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]interface{}, 0)
	for rows.Next() {
		var v interface{}
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return result, nil
}
