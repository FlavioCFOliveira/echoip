package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
)

func main() {
	var host string
	flag.StringVar(&host, "host", "", "the hostname")

	var port int
	flag.IntVar(&port, "port", 8010, "the port")
	flag.Parse()

	http.HandleFunc("/", homeHandler)

	log.Printf("Starting echo-ip service on %s:%v", host, port)

	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%v", host, port), nil))
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, ReadUserIP(r))
}

func ReadUserIP(r *http.Request) string {

	IPAddress := r.Header.Get("X-Real-Ip")

	if len(IPAddress) == 0 {

		IPAddress = r.Header.Get("X-Forwarded-For")

		if len(IPAddress) == 0 {
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			IPAddress = ip
		}

	}

	return IPAddress
}
