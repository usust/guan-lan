// gateway is a deliberately small transport relay. It accepts no control API,
// executes no commands, and connects only to destinations supplied at creation.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

type route struct {
	Listen   int
	Target   string
	Protocol string
}

func main() {
	var routes []route
	if len(os.Args) != 2 || json.Unmarshal([]byte(os.Args[1]), &routes) != nil || len(routes) == 0 {
		log.Fatal("invalid fixed routes")
	}
	var wg sync.WaitGroup
	for _, r := range routes {
		if r.Listen < 1024 || r.Listen > 65535 {
			log.Fatal("invalid port")
		}
		if _, _, err := net.SplitHostPort(r.Target); err != nil {
			log.Fatal(err)
		}
		wg.Add(1)
		go func(r route) {
			defer wg.Done()
			if r.Protocol == "tcp" {
				tcp(r)
			} else if r.Protocol == "udp" {
				udp(r)
			} else {
				log.Fatal("invalid protocol")
			}
		}(r)
	}
	wg.Wait()
}
func tcp(r route) {
	deadline := time.Now().Add(25 * time.Second)
	for {
		conn, e := net.DialTimeout("tcp", r.Target, time.Second)
		if e == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			log.Fatal("target unavailable: ", r.Target)
		}
		time.Sleep(250 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(r.Listen))
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	slots := make(chan struct{}, 128)
	for {
		client, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				defer client.Close()
				upstream, err := net.DialTimeout("tcp", r.Target, 3*time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				client.SetDeadline(time.Now().Add(10 * time.Minute))
				upstream.SetDeadline(time.Now().Add(10 * time.Minute))
				done := make(chan struct{})
				go func() {
					io.Copy(upstream, client)
					if c, ok := upstream.(*net.TCPConn); ok {
						c.CloseWrite()
					}
					close(done)
				}()
				io.Copy(client, upstream)
				client.Close()
				<-done
			}()
		default:
			client.Close()
		}
	}
}
func udp(r route) {
	address, err := net.ResolveUDPAddr("udp", ":"+strconv.Itoa(r.Listen))
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.ListenUDP("udp", address)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	target, err := net.ResolveUDPAddr("udp", r.Target)
	if err != nil {
		log.Fatal(err)
	}
	type peer struct {
		conn *net.UDPConn
		last time.Time
	}
	peers := map[string]*peer{}
	var mu sync.Mutex
	buffer := make([]byte, 65535)
	for {
		n, client, err := listener.ReadFromUDP(buffer)
		if err != nil {
			log.Fatal(err)
		}
		key := client.String()
		mu.Lock()
		p := peers[key]
		if p == nil {
			if len(peers) >= 128 {
				mu.Unlock()
				continue
			}
			conn, err := net.DialUDP("udp", nil, target)
			if err != nil {
				mu.Unlock()
				continue
			}
			p = &peer{conn: conn}
			peers[key] = p
			go func(p *peer, client *net.UDPAddr, key string) {
				defer p.conn.Close()
				defer func() { mu.Lock(); delete(peers, key); mu.Unlock() }()
				reply := make([]byte, 65535)
				for {
					p.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
					n, err := p.conn.Read(reply)
					if err != nil {
						return
					}
					listener.WriteToUDP(reply[:n], client)
				}
			}(p, client, key)
		}
		p.last = time.Now()
		p.conn.Write(buffer[:n])
		mu.Unlock()
	}
}
