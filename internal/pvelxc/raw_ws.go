package pvelxc

import (
	"sync"

	"github.com/gorilla/websocket"
)

// BridgeWebsocketPair copies messages between two gorilla websocket connections until one side errors.
// Message types (binary vs text) are preserved in both directions.
func BridgeWebsocketPair(a, b *websocket.Conn) {
	errSig := make(chan struct{}, 1)
	var once sync.Once
	signal := func() {
		once.Do(func() { errSig <- struct{}{} })
	}

	forward := func(src, dst *websocket.Conn) {
		defer signal()
		for {
			mt, p, err := src.ReadMessage()
			if err != nil {
				return
			}
			if err := dst.WriteMessage(mt, p); err != nil {
				return
			}
		}
	}

	go forward(a, b)
	go forward(b, a)
	<-errSig
	_ = a.Close()
	_ = b.Close()
}

// BridgeVNCWebSockets copies RFB-over-WebSocket between browser and PVE. Proxmox may use Text frames for
// ASCII parts of the handshake; noVNC expects binary ArrayBuffers, so we always write BinaryMessage to the browser.
func BridgeVNCWebSockets(browser, pve *websocket.Conn) {
	errSig := make(chan struct{}, 1)
	var once sync.Once
	signal := func() {
		once.Do(func() { errSig <- struct{}{} })
	}

	pveToBrowser := func() {
		defer signal()
		for {
			mt, p, err := pve.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
				if err := browser.WriteMessage(websocket.BinaryMessage, p); err != nil {
					return
				}
				continue
			}
			if err := browser.WriteMessage(mt, p); err != nil {
				return
			}
		}
	}

	browserToPVE := func() {
		defer signal()
		for {
			mt, p, err := browser.ReadMessage()
			if err != nil {
				return
			}
			outMt := mt
			outP := p
			if mt == websocket.TextMessage {
				outMt = websocket.BinaryMessage
			}
			if err := pve.WriteMessage(outMt, outP); err != nil {
				return
			}
		}
	}

	go pveToBrowser()
	go browserToPVE()
	<-errSig
	_ = browser.Close()
	_ = pve.Close()
}
