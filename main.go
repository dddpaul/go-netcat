package main

import (
	"flag"
	"io"
	"log"
	"net"
	"os"
)

const (
	// BufferLimit specifies buffer size that is sufficient to handle full-size UDP datagram or TCP segment in one step
	BufferLimit = 2<<16 - 1
	// UDPDisconnectSequence is used to disconnect UDP sessions
	UDPDisconnectSequence = "~."
)

// Progress indicates transfer status
type Progress struct {
	remoteAddr net.Addr
	bytes      uint64
}

// TransferStreams launches two read-write goroutines and waits for signal from them
func TransferStreams(con net.Conn) {
	c := make(chan Progress)

	// Read from Reader and write to Writer until EOF
	copy := func(r io.ReadCloser, w io.WriteCloser) {
		defer func() {
			r.Close()
			w.Close()
		}()
		n, err := io.Copy(w, r)
		if err != nil {
			log.Printf("[%s]: ERROR: %s\n", con.RemoteAddr(), err)
		}
		c <- Progress{bytes: uint64(n)}
	}

	go copy(con, os.Stdout)
	go copy(os.Stdin, con)

	p := <-c
	log.Printf("[%s]: Connection has been closed by remote peer, %d bytes has been received\n", con.RemoteAddr(), p.bytes)
	p = <-c
	log.Printf("[%s]: Local peer has been stopped, %d bytes has been sent\n", con.RemoteAddr(), p.bytes)
}

// TransferPackets launches receive goroutine first, wait for address from it (if needed), launches send goroutine then
func TransferPackets(con net.Conn) {
	c := make(chan Progress)

	// Read from Reader and write to Writer until EOF.
	// ra is an address to whom packets must be sent in listen mode.
	copy := func(r io.ReadCloser, w io.WriteCloser, ra net.Addr) {
		defer func() {
			r.Close()
			w.Close()
		}()

		buf := make([]byte, BufferLimit)
		bytes := uint64(0)
		var n int
		var err error
		var addr net.Addr

		for {
			// Read
			if con, ok := r.(*net.UDPConn); ok {
				n, addr, err = con.ReadFrom(buf)
				// In listen mode remote address is unknown until read from connection.
				// So we must inform caller function with received remote address.
				if con.RemoteAddr() == nil && ra == nil {
					ra = addr
					c <- Progress{remoteAddr: ra}
				}
			} else {
				n, err = r.Read(buf)
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[%s]: ERROR: %s\n", ra, err)
				}
				break
			}
			if string(buf[0:n-1]) == UDPDisconnectSequence {
				break
			}

			// Write
			if con, ok := w.(*net.UDPConn); ok && con.RemoteAddr() == nil {
				// Connection remote address must be nil otherwise "WriteTo with pre-connected connection" will be thrown
				n, err = con.WriteTo(buf[0:n], ra)
			} else {
				n, err = w.Write(buf[0:n])
			}
			if err != nil {
				log.Printf("[%s]: ERROR: %s\n", ra, err)
				break
			}
			bytes += uint64(n)
		}
		c <- Progress{bytes: bytes}
	}

	ra := con.RemoteAddr()
	go copy(con, os.Stdout, ra)
	// If connection hasn't got remote address then wait for it from receiver goroutine
	if ra == nil {
		p := <-c
		ra = p.remoteAddr
		log.Printf("[%s]: Datagram has been received\n", ra)
	}
	go copy(os.Stdin, con, ra)

	p := <-c
	log.Printf("[%s]: Connection has been closed, %d bytes has been received\n", ra, p.bytes)
	p = <-c
	log.Printf("[%s]: Local peer has been stopped, %d bytes has been sent\n", ra, p.bytes)
}

func main() {
	var host, port, proto string
	var listen bool
	flag.StringVar(&host, "host", "", "Remote host to connect, i.e. 127.0.0.1")
	flag.StringVar(&proto, "proto", "tcp", "TCP/UDP mode")
	flag.BoolVar(&listen, "listen", false, "Listen mode")
	flag.StringVar(&port, "port", ":9999", "Port to listen on or connect to (prepended by colon), i.e. :9999")
	flag.Parse()

	startTCPServer := func() {
		ln, err := net.Listen(proto, port)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("Listening on", proto+port)
		con, err := ln.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		log.Printf("[%s]: Connection has been opened\n", con.RemoteAddr())
		TransferStreams(con)
	}

	startTCPClient := func() {
		con, err := net.Dial(proto, host+port)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("Connected to", host+port)
		TransferStreams(con)
	}

	startUDPServer := func() {
		addr, err := net.ResolveUDPAddr(proto, port)
		if err != nil {
			log.Fatalln(err)
		}
		con, err := net.ListenUDP(proto, addr)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("Listening on", proto+port)
		// This connection doesn't know remote address yet
		TransferPackets(con)
	}

	startUDPClient := func() {
		addr, err := net.ResolveUDPAddr(proto, host+port)
		if err != nil {
			log.Fatalln(err)
		}
		con, err := net.DialUDP(proto, nil, addr)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("Sending datagrams to", host+port)
		TransferPackets(con)
	}

	switch proto {
	case "tcp":
		if listen {
			startTCPServer()
		} else if host != "" {
			startTCPClient()
		} else {
			flag.Usage()
		}
	case "udp":
		if listen {
			startUDPServer()
		} else if host != "" {
			startUDPClient()
		} else {
			flag.Usage()
		}
	default:
		flag.Usage()
	}
}