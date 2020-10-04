package main

type session struct {
	clients []*ClientConn
	master  *ClientConn
	counter int
}
