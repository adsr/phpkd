package main

// #include "phpkd.h"
import "C"

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type server struct {
	numWorkers  int
	httpAddr    string
	phpHandler  string
	workers     map[int]*worker
	httpServer  *http.Server
	workerQueue chan *worker
	workerWg    sync.WaitGroup
}

type worker struct {
	id int
	*server
	w            http.ResponseWriter
	r            *http.Request
	statusCode   int
	bytesWritten int
	requestChan  chan request
	requestDone  chan bool
	scratchBuf   *bytes.Buffer
}

type request struct {
	w http.ResponseWriter
	r *http.Request
}

type kv struct {
	k string
	v string
}

const PHPKD_VERSION = "phpkd/0.1.0"

var (
	srv     *server
	errLog  *log.Logger
	infoLog *log.Logger
)

func main() {
	logFlags := log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC
	errLog = log.New(os.Stderr, "[E] ", logFlags)
	infoLog = log.New(os.Stdout, "[I] ", logFlags)

	srv = &server{}
	flag.StringVar(&srv.httpAddr, "addr", ":8080", "listen address")
	flag.IntVar(&srv.numWorkers, "threads", runtime.NumCPU(), "number of worker threads")
	flag.StringVar(&srv.phpHandler, "handler", "index.php", "PHP handler path")
	flag.Parse()
	srv.run()
}

func (self *server) run() {
	// Init PHP SAPI
	C.phpkd_init(C.CString(self.phpHandler)) // Freed in phpkd_deinit

	// Init worker queue
	self.workerQueue = make(chan *worker, self.numWorkers)

	// Init and run workers
	self.workers = make(map[int]*worker)
	for workerId := 0; workerId < self.numWorkers; workerId++ {
		worker := &worker{id: workerId, server: self}
		self.workers[workerId] = worker
		self.workerWg.Add(1)
		go worker.run()
	}

	// It would be nice to disable all the default signal handling in PHP and
	// Go. Calling `self.handleSignals()` above `C.phpkd_init()` does not have
	// the desired effect for some reason. This seems to help, maybe because
	// launching a goroutine messes with signal handlers? A real solution
	// remains a TODO.
	for len(self.workerQueue) < self.numWorkers {
		time.Sleep(100 * time.Millisecond)
	}
	self.handleSignals()

	// Start net/http server
	self.httpServer = &http.Server{
		Addr:    self.httpAddr,
		Handler: self,
	}
	infoLog.Printf("server.run: ListenAndServe\n")
	err := self.httpServer.ListenAndServe()
	infoLog.Printf("server.run: ListenAndServe finish=%+v\n", err)

	for _, worker := range self.workers {
		close(worker.requestChan)
	}
	self.workerWg.Wait()
	infoLog.Printf("server.run: Workers finished\n")

	// Deinit PHP SAPI
	C.phpkd_deinit()
}

func (self *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// This is a bit awkward as a result of the following constraints:
	//
	// - PHP workers need to run on a single OS thread. We cannot hop between
	//   threads for different requests. (This is why we call
	//   `runtime.LockOSThread` in `worker::run`.)
	// - I haven't found a way make net/http invoke `ServeHTTP` on a specific
	//   goroutine. It will invoke on whatever goroutines/threads net/http
	//   spawned internally.
	// - net/http w and r are only valid for the span of a single call to
	//   ServerHTTP. We cannot dispatch a pair of w,r for another goroutine to
	//   handle asynchronously.
	//
	// So here is the solution I came up with. (There is probably a better way.)
	// We use `workerQueue` as a thread-safe queue of idle workers. After
	// acquiring a worker from the queue, we tell it to handle a request via
	// `requestChan`. The worker then signals to us that it's done via
	// `requestDone`. Finally we place the worker back in the `workerQueue`.
	worker := <-self.workerQueue
	worker.requestChan <- request{w, r}
	<-worker.requestDone
	self.workerQueue <- worker
}

func (self *server) handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan)
	go func() {
		for sig := range sigChan {
			errLog.Printf("handleSignals: Received signal: %+v\n", sig)
			// TODO Actually handle signals
			// https://httpd.apache.org/docs/2.4/stopping.html
			switch sig {
			case syscall.SIGTERM:
				fallthrough
			case syscall.SIGINT: // hard stop
				self.httpServer.Shutdown(context.Background())
			case syscall.SIGWINCH: // TODO graceful stop
			case syscall.SIGHUP: // TODO hard restart
			case syscall.SIGUSR1: // TODO graceful restart
			}
		}
	}()
}

func (self *worker) run() {
	// Exclusively lock this goroutine to a single OS thread
	runtime.LockOSThread()

	// Init channels
	self.requestChan = make(chan request)
	self.requestDone = make(chan bool)

	// Queue ourselves up for work
	self.server.workerQueue <- self

	// Loop over requests
	for req := range self.requestChan {
		// Reset worker state
		self.w = req.w
		self.r = req.r
		self.statusCode = 0
		self.bytesWritten = 0

		// Call into PHP
		C.phpkd_request(
			C.int(self.id),
			C.int(self.r.ProtoMajor*1000+self.r.ProtoMinor),
			C.CString(self.r.Method), // These are all freed in phpkd_request
			C.CString(self.r.RequestURI),
			C.CString(self.r.URL.RawQuery),
			C.CBytes(self.buildHeaderData()),
			C.CBytes(self.buildSvarData()),
		)

		// If nothing was written and there is a response code, write it now
		if self.bytesWritten <= 0 && self.statusCode != 0 {
			self.w.WriteHeader(self.statusCode)
		}

		// Emit NCSA Common Log Format
		fmt.Printf("%s - - [%s] - \"%s %s %s\" %d %d\n",
			self.r.RemoteAddr,
			time.Now().Format("02/Jan/2006:15:04:05 -0700"),
			self.r.Method,
			self.r.RequestURI,
			self.r.Proto,
			self.statusCode, // TODO
			self.bytesWritten,
		)

		// Signal to `ServeHTTP` that we are done
		self.requestDone <- true
	}

	self.workerWg.Done()
}

func (self *worker) getAndResetScratchBuf() *bytes.Buffer {
	if self.scratchBuf == nil {
		self.scratchBuf = &bytes.Buffer{}
	}
	self.scratchBuf.Reset()
	return self.scratchBuf
}

func (self *worker) buildHeaderData() []byte {
	headers := make([]kv, 0)
	for h, vs := range self.r.Header {
		for _, v := range vs {
			headers = append(headers, kv{h, v})
		}
	}
	return self.buildKeyValData(headers)
}

func (self *worker) buildSvarData() []byte {
	svars := make([]kv, 0)
	for h, vs := range self.r.Header {
		k := ""
		switch strings.ToLower(h) {
		case "host":
			k = "HTTP_HOST"
		case "user-agent":
			k = "HTTP_USER_AGENT"
		case "accept":
			k = "HTTP_ACCEPT"
		case "cookie":
			k = "HTTP_COOKIE"
		case "content-length":
			k = "HTTP_CONTENT_LENGTH"
		case "content-type":
			k = "CONTENT_TYPE"
		}
		if k != "" {
			v := vs[len(vs)-1]
			svars = append(svars, kv{k, v})
		}
	}
	svars = append(svars, kv{"SERVER_SOFTWARE", PHPKD_VERSION})
	// svars = append(svars, kv{"SERVER_NAME", ...})
	// svars = append(svars, kv{"SERVER_ADDR", ...})c
	// svars = append(svars, kv{"SERVER_PORT", ...})
	remoteAddr := strings.SplitN(self.r.RemoteAddr, ":", 2)
	svars = append(svars, kv{"REMOTE_ADDR", remoteAddr[0]})
	if len(remoteAddr) > 1 {
		svars = append(svars, kv{"REMOTE_PORT", remoteAddr[1]})
	}
	// svars = append(svars, kv{"DOCUMENT_ROOT", ...})
	svars = append(svars, kv{"REQUEST_SCHEME", self.r.URL.Scheme})
	// svars = append(svars, kv{"SERVER_ADMIN", ...})
	svars = append(svars, kv{"SERVER_PROTOCOL", self.r.Proto})
	svars = append(svars, kv{"REQUEST_METHOD", self.r.Method})
	svars = append(svars, kv{"QUERY_STRING", self.r.URL.RawQuery})
	svars = append(svars, kv{"REQUEST_URI", self.r.RequestURI})
	svars = append(svars, kv{"SCRIPT_NAME", self.phpHandler})

	return self.buildKeyValData(svars)
}

// Since we are limited to passing scalars between C and Go, we use a simple
// key-val binary format:
//
//   <8>       num_key_vals
//   <8>       key_len
//   <key_len> key
//   <8>       val_len
//   <val_len> val
//   ...
func (self *worker) buildKeyValData(kvs []kv) []byte {
	buf := self.getAndResetScratchBuf()
	binary.Write(buf, binary.LittleEndian, uint64(len(kvs)))
	for _, pair := range kvs {
		bk := []byte(pair.k)
		bv := []byte(pair.v)
		binary.Write(buf, binary.LittleEndian, uint64(len(bk)+1))
		binary.Write(buf, binary.LittleEndian, bk)
		binary.Write(buf, binary.LittleEndian, byte(0))
		binary.Write(buf, binary.LittleEndian, uint64(len(bv)+1))
		binary.Write(buf, binary.LittleEndian, bv)
		binary.Write(buf, binary.LittleEndian, byte(0))
	}
	return buf.Bytes()
}

//export worker_php_ub_write
func worker_php_ub_write(id C.int, cstr *C.char, sz C.size_t) C.size_t {
	worker := worker_get(int(id))
	if worker == nil {
		return 0
	}
	str := C.GoStringN(cstr, C.int(sz))
	bytes := []byte(str)
	if worker.bytesWritten <= 0 && worker.statusCode != 0 {
		worker.w.WriteHeader(worker.statusCode)
		worker.statusCode = 0
	}
	nBytes, err := worker.w.Write(bytes)
	worker.bytesWritten += nBytes
	if err != nil {
		errLog.Printf("worker_php_ub_write: id=%d err=%+v\n", id, err)
	}
	return C.size_t(nBytes)
}

//export worker_php_send_header
func worker_php_send_header(id C.int, cstr *C.char, sz C.size_t) {
	worker := worker_get(int(id))
	if worker == nil {
		return
	}
	header := C.GoStringN(cstr, C.int(sz))

	// PHP SAPI passes headers like so:
	//
	//   "HTTP/1.1 200 OK"
	//   "Content-Type: text/html; charset=UTF-8"
	//   "X-Powered-By: PHP/8.2.0-dev"
	//   ...
	//
	// So we need to do some minimal parsing to make this work with the
	// net/http API.
	headerParts := strings.SplitN(header, " ", 2)
	if len(headerParts) < 2 {
		headerParts = append(headerParts, "")
	}

	if strings.HasPrefix(headerParts[0], "HTTP/") {
		// Expect format: "HTTP/<ignore>" <space> <response_code> <space> <ignore>"
		headerParts2 := strings.SplitN(headerParts[1], " ", 2)
		statusCode, _ := strconv.Atoi(headerParts2[0])
		if statusCode < 100 || statusCode >= 600 {
			statusCode = 500
		}
		// Appears the order needs to be:
		//
		//   w.Header().Set
		//   w.WriteHeader
		//   w.Write
		//
		// So don't call WriteHeader yet. Save for later.
		worker.statusCode = statusCode
	} else if strings.HasSuffix(headerParts[0], ":") {
		// Expect format: "<header_name>: <header_value>"
		headerName := headerParts[0][0 : len(headerParts[0])-1]
		worker.w.Header().Set(headerName, headerParts[1])
	}
}

//export worker_php_log_message
func worker_php_log_message(id C.int, cstr *C.char, syslogType int) {
	worker := worker_get(int(id))
	if worker == nil {
		return
	}
	message := C.GoString(cstr)
	var xLog *log.Logger
	if syslogType >= 5 { // See sys/syslog.h
		xLog = infoLog
	} else {
		xLog = errLog
	}
	xLog.Printf("worker_php_log_message: id=%d log=%s\n", id, message)
}

//export worker_php_read_post
func worker_php_read_post(id C.int, nBytes C.size_t, readBuf *unsafe.Pointer, nRead *C.size_t) {
	worker := worker_get(int(id))
	if worker == nil || worker.r == nil {
		*nRead = C.size_t(0)
		return
	}
	buf := worker.getAndResetScratchBuf()
	writtenN, _ := io.CopyN(buf, worker.r.Body, int64(nBytes))
	*nRead = C.size_t(writtenN)
	*readBuf = C.CBytes(buf.Bytes()) // Freed in phpkd_read_post
}

func worker_get(id int) *worker {
	worker := srv.workers[id]
	if worker == nil {
		errLog.Printf("worker_get: Invalid worker id %d\n", id)
		return nil
	}
	return worker
}
