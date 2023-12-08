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

type ConfigsHolder struct {
	configs       map[string]*ConfigHolder
	activeConfigs int
}

func NewConfigsHolder() *ConfigsHolder {
	return &ConfigsHolder{
		configs: make(map[string]*ConfigHolder),
	}
}

func (ch *ConfigsHolder) add(id string, config *ConfigHolder) error {
	confExists := false
	if config, exists := ch.configs[id]; exists {
		confExists = true
		if config.conn != nil {
			return fmt.Errorf("Active config for the same secret already exists")
		}
	}
	ch.configs[id] = config
	if !confExists {
		fmt.Println("Added config for ", id)
		ch.activeConfigs += 1
	} else {
		fmt.Println("Replaced config for ", id)
	}
	middlewares.ActiveConfigs.Set(float64(ch.activeConfigs))
	return nil
}

func (ch *ConfigsHolder) get(id string) *ConfigHolder {
	config, ok := ch.configs[id]
	if !ok {
		return nil
	}
	return config
}

func (ch *ConfigsHolder) delete(id string) {
	configHolder, ok := ch.configs[id]
	if ok && configHolder.conn != nil {
		configHolder.conn.Close()
	}
	delete(ch.configs, id)
	fmt.Println("Deleted config for ", id)
	ch.activeConfigs -= 1
	middlewares.ActiveConfigs.Set(float64(ch.activeConfigs))
}

var configs *ConfigsHolder
var upgrader = websocket.Upgrader{}

func main() {
	configs = NewConfigsHolder()

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

	id := generateID(secret)
	err = configs.add(id, &ConfigHolder{content: content})
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}

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

	id := generateID(secret)

	configHolder := configs.get(id)
	if configHolder != nil {
		configHolder.conn = conn
		fmt.Println("Websocket connection for ", id)
		defer configs.delete(id)
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

	config := configs.get(id)
	if config == nil {
		// If no config is found, return the DIRECT PAC content
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(directPacContent))
		return
	}

	w.Header().Set("Content-Type", "application/javascript")
	w.Write(config.content)

	sendMessageToSocket(
		config,
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
