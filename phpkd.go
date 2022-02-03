package main

// #include "phpkd.h"
import "C"

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type server struct {
	numWorkers  int
	httpAddr    string
	workers     map[int]*worker
	httpServer  *http.Server
	workerQueue chan *worker
}

type worker struct {
	id int
	*server
	w            http.ResponseWriter
	r            *http.Request
	statusCode   int
	requestChan chan bool
	requestDone  chan bool
}

type request struct {
	w http.ResponseWriter
	r *http.Request
}

var srv *server

func main() {
	srv = &server{}
	flag.StringVar(&srv.httpAddr, "addr", ":8080", "listen address")
	flag.IntVar(&srv.numWorkers, "threads", 4, "number of worker threads")
	flag.Parse()
	srv.run()
}

func (self *server) run() {
	C.phpkd_init()

	self.workerQueue = make(chan *worker, self.numWorkers)

	self.workers = make(map[int]*worker)
	for workerId := 0; workerId < self.numWorkers; workerId++ {
		worker := &worker{id: workerId, server: self}
		self.workers[workerId] = worker
		go worker.run()
	}

	self.httpServer = &http.Server{
		Addr:    self.httpAddr,
		Handler: self,
	}

	self.httpServer.ListenAndServe()
}

func (self *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	worker := <-self.workerQueue
	worker.w = w
	worker.r = r
	worker.requestChan <- true
	<-worker.requestDone
	self.workerQueue <- worker
}

func (self *worker) run() {
	runtime.LockOSThread()
	self.requestChan = make(chan bool)
	self.requestDone = make(chan bool)
	self.workerQueue <- self
	for _ = range self.requestChan {
		C.phpkd_request(C.int(self.id))
		self.requestDone <- true
		self.reset()
	}
}

func (self *worker) reset() {
	self.w = nil
	self.r = nil
	self.statusCode = 0
}

//export worker_php_ub_write
func worker_php_ub_write(id C.int, cstr *C.char, sz C.size_t) C.size_t {
	worker := worker_get(int(id))
	if worker == nil {
		return 0
	}
	str := C.GoStringN(cstr, C.int(sz))
	bytes := []byte(str)
	if worker.statusCode != 0 {
		worker.w.WriteHeader(worker.statusCode)
	}
	nbytes, err := worker.w.Write(bytes)
	fmt.Fprintf(os.Stderr, "worker_php_ub_write: id=%d err=%+v\n", id, err)
	return C.size_t(nbytes)
}

//export worker_php_send_header
func worker_php_send_header(id C.int, cstr *C.char, sz C.size_t) {
	worker := worker_get(int(id))
	if worker == nil {
		return
	}
	header := C.GoStringN(cstr, C.int(sz))
	headerParts := strings.SplitN(header, " ", 2)
	if len(headerParts) < 2 {
		headerParts = append(headerParts, "")
	}
	fmt.Fprintf(os.Stderr, "worker_php_send_header: headerParts=%+v\n", headerParts)
	if strings.HasPrefix(headerParts[0], "HTTP") {
		headerPartsParts := strings.SplitN(headerParts[1], " ", 2)
		statusCode := 500
		statusCode, _ = strconv.Atoi(headerPartsParts[0])
		if statusCode < 100 || statusCode >= 600 {
			statusCode = 500
		}
		worker.statusCode = statusCode
		// worker.w.WriteHeader(statusCode)
	} else if strings.HasSuffix(headerParts[0], ":") {
		headerName := headerParts[0][0 : len(headerParts[0])-1]
		fmt.Fprintf(os.Stderr, "Set(%s, %s)\n", headerName, headerParts[1])
		worker.w.Header().Set(headerName, headerParts[1])
	}

	// TODO Appears the order needs to be
	//   Header().Set
	//   WriteHeader
	//   Write
}

func worker_get(id int) *worker {
	worker := srv.workers[id]
	if worker == nil {
		fmt.Fprintf(os.Stderr, "worker_get: invalid worker id %d\n", id)
		return nil
	}
	return worker
}
