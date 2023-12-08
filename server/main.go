package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"mitmpac/server/middlewares"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	directPacContent = `function FindProxyForURL(url, host) {
    return "DIRECT";
}`
)

type ConfigHolder struct {
	content []byte
	conn    *websocket.Conn
	mutex   sync.Mutex
}

var configs map[string]*ConfigHolder
var upgrader = websocket.Upgrader{}

func main() {
	configs = make(map[string]*ConfigHolder)

	router := chi.NewRouter()

	mitmpacHandlers := router.With(middlewares.MetricsMiddleware)

	mitmpacHandlers.Post("/upload", uploadHandler)
	mitmpacHandlers.Get("/ws", wsHandler)
	mitmpacHandlers.Get("/pac/{pac_id}", pacHandlerWithID)

	middlewares.SetDefaultRoutesMetrics(mitmpacHandlers.Routes())

	router.Get("/metrics", promhttp.Handler().ServeHTTP)

	fmt.Println("PAC server is listening on port 8008.")
	http.ListenAndServe(":8008", router)
}

func generateID(secret string) string {
	hash := sha1.New()
	hash.Write([]byte(secret))
	return hex.EncodeToString(hash.Sum(nil))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid method, use POST", http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	secret := r.Header.Get("X-Secret")
	if secret == "" {
		http.Error(w, "Missing X-Secret header", http.StatusBadRequest)
		return
	}
	fmt.Println("Secret", secret)

	id := generateID(secret)
	configs[id] = &ConfigHolder{
		content: content,
	}

	fmt.Printf("Uploaded PAC for %s\n", id)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(id))
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Failed to upgrade websocket:", err)
		return
	}

	secret := r.Header.Get("X-Secret")
	if secret == "" {
		conn.WriteMessage(websocket.TextMessage, []byte("Missing X-Secret header"))
		conn.Close()
		return
	}
	fmt.Println("Secret", secret)

	id := generateID(secret)

	configHolder, ok := configs[id]
	if ok {
		configHolder.conn = conn
		fmt.Printf("Websocket connection for %s\n", id)
		defer deleteConfig(id)
	} else {
		conn.WriteMessage(websocket.TextMessage, []byte("Invalid X-Secret"))
		conn.Close()
		return
	}

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func pacHandlerWithID(w http.ResponseWriter, r *http.Request) {
	// Get the config ID from the path "/pac/{configID}"
	id := strings.TrimPrefix(r.URL.Path, "/pac/")
	if id == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	clientIP := getClientIP(r)
	fmt.Printf("Config %s accessed by %s %s\n", id, clientIP, r.UserAgent())

	configHolder, ok := configs[id]
	if !ok {
		// If no config is found, return the DIRECT PAC content
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(directPacContent))
		return
	}

	w.Header().Set("Content-Type", "application/javascript")
	w.Write(configHolder.content)

	sendMessageToSocket(
		configHolder,
		fmt.Sprintf("Config accessed by %s %s", clientIP, r.UserAgent()),
	)
}

func getClientIP(r *http.Request) string {
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func sendMessageToSocket(holder *ConfigHolder, message string) {
	if holder.conn == nil {
		return
	}
	holder.mutex.Lock()
	defer holder.mutex.Unlock()
	err := holder.conn.WriteMessage(websocket.TextMessage, []byte(message))
	if err != nil {
		fmt.Printf("Failed to send message to websocket: %v\n", err)
	}
}

func deleteConfig(id string) {
	configHolder, ok := configs[id]
	if ok && configHolder.conn != nil {
		configHolder.conn.Close()
	}
	delete(configs, id)
	fmt.Printf("Deleted config for %s\n", id)
}
