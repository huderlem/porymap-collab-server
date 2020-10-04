package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"
	"os"
)

var sessions = map[string]*session{}

var clientMessageSignature uint32 = 0x12345678
var serverMessageSignature uint32 = 0x98765432

// Server message types.
const (
	ServerMessageCreatedSession   = 0x1
	ServerMessageJoinedSession    = 0x2
	ServerMessageBroadcastCommand = 0x3
)

// ClientConn holds the client connection details.
type ClientConn struct {
	Conn        net.Conn
	SessionName string
}

func handleConnection(conn net.Conn) {
	log.Printf("Serving new client %s\n", conn.RemoteAddr().String())
	readBuffer := make([]byte, 4096)
	workBuffer := new(bytes.Buffer)
	client := &ClientConn{Conn: conn}
	for {
		numBytes, err := conn.Read(readBuffer)
		if err != nil {
			log.Println("Connection closed by client...")
			break
		}

		if numBytes == 0 {
			continue
		}

		workBuffer.Write(readBuffer[:numBytes])
		terminate := client.processMessageBuffer(workBuffer)
		if terminate {
			break
		}
	}
	log.Printf("Client %s disconnected\n", conn.RemoteAddr().String())
	conn.Close()

	if session, ok := sessions[client.SessionName]; ok {
		if client == session.master {
			log.Printf("Tearing down session %s\n", client.SessionName)
			for _, c := range session.clients {
				if err := c.Conn.Close(); err != nil {
					log.Printf("Failed to close client connection: %s\n", err)
				}
			}
			delete(sessions, client.SessionName)
			log.Printf("Num sessions remaining: %d\n", len(sessions))
		} else {
			for i, c := range session.clients {
				if c == client {
					session.clients[i] = session.clients[len(session.clients)-1]
					session.clients[len(session.clients)-1] = nil
					session.clients = session.clients[:len(session.clients)-1]
					break
				}
			}
			log.Printf("Removed client from session %s disconnected\n", client.SessionName)
		}
	}
}

func (client *ClientConn) processMessageBuffer(buff *bytes.Buffer) bool {
	for {
		if buff.Len() < 12 {
			return false
		}

		signature := binary.LittleEndian.Uint32(buff.Bytes()[:4])
		if signature != clientMessageSignature {
			log.Printf("Incorrect message signature. Disconnecting...")
			return true
		}

		payloadSize := binary.LittleEndian.Uint32(buff.Bytes()[4:8])
		messageSize := int(12 + payloadSize)
		if buff.Len() < messageSize {
			return false
		}

		messageType := binary.LittleEndian.Uint32(buff.Bytes()[8:12])
		message := buff.Next(messageSize)
		client.processMessage(messageType, message[12:])
	}
}

func (client *ClientConn) processMessage(messageType uint32, message []byte) {
	switch messageType {
	case 0x1: // Start collab session
		sessionName := string(message)
		if _, ok := sessions[sessionName]; ok {
			log.Printf("Session '%s' already exists.", sessionName)
			return
		}
		client.SessionName = sessionName
		sessions[sessionName] = &session{
			clients: []*ClientConn{client},
			master:  client,
		}
		log.Printf("Created new session '%s'", sessionName)
	case 0x2: // Join existing collab session
		sessionName := string(message)
		if _, ok := sessions[sessionName]; !ok {
			log.Printf("Session '%s' doesn't exist.", sessionName)
			return
		}
		shouldAddClient := true
		for _, c := range sessions[sessionName].clients {
			if c == client {
				log.Printf("Client is already in session '%s'", sessionName)
				shouldAddClient = false
			}
		}
		if shouldAddClient {
			client.SessionName = sessionName
			sessions[sessionName].clients = append(sessions[sessionName].clients, client)
			log.Printf("Client joined session '%s'", sessionName)
		}
	case 0x3: // Broadcast text message
		go client.broadcastToOthers(message)
	}
}

func prepareServerMessage(message []byte, messageType int) []byte {
	header := []byte{
		byte(serverMessageSignature & 0xFF),
		byte((serverMessageSignature >> 8) & 0xFF),
		byte((serverMessageSignature >> 16) & 0xFF),
		byte((serverMessageSignature >> 24) & 0xFF),
		byte(len(message) & 0xFF),
		byte((len(message) >> 8) & 0xFF),
		byte((len(message) >> 16) & 0xFF),
		byte((len(message) >> 24) & 0xFF),
		byte(messageType & 0xFF),
		byte((messageType >> 8) & 0xFF),
		byte((messageType >> 16) & 0xFF),
		byte((messageType >> 24) & 0xFF),
	}
	return append(header, message...)
}

func (client *ClientConn) broadcastToOthers(message []byte) {
	serverMessage := prepareServerMessage(message, ServerMessageBroadcastCommand)
	for _, sessionClient := range sessions[client.SessionName].clients {
		if sessionClient != client {
			go func(m []byte, c *ClientConn) {
				if _, err := c.Conn.Write(m); err != nil {
					log.Printf("Failed to broadcast message. %s", err)
				}
			}(serverMessage, sessionClient)
		}
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
		log.Printf("Defaulting to port %s", port)
	}
	log.Printf("Starting Porymap collab server on port %s...\n", port)

	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Println(err)
		return
	}
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			log.Println(err)
			return
		}
		go handleConnection(c)
	}
}
