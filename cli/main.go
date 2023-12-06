package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	wifiname "github.com/yelinaung/wifi-name"
)

const (
	uploadURL        = "http://127.0.0.1:8008/upload"
	wsURL            = "ws://127.0.0.1:8008/ws"
	pacURLFormat     = "http://127.0.0.1:8008/pac/%s"
	defaultProxyPort = "8080"
	pacFormat        = `function FindProxyForURL(url, host) {
    return "PROXY %s:%s";
}`
)

func main() {
	secret := flag.String("secret", "", "unique secret value to identify the user")
	port := flag.String("port", "", "target port of proxy")
	flag.Parse()

	if *secret == "" {
		fmt.Println("Please provide a unique secret value with --secret")
		return
	}

	proxyPort := defaultProxyPort
	if *port != "" {
		proxyPort = *port
	}

	ip := getLocalIP()
	networkName := wifiname.WifiName()
	if ip == "" {
		fmt.Println("Failed to get local IP.")
		return
	}

	fmt.Printf("Network name: %s\n", networkName)
	fmt.Printf("Local IP: %s\n", ip)

	pacContent := fmt.Sprintf(pacFormat, ip, proxyPort)

	fmt.Printf("Generated PAC configuration: \n\n%s\n\n", pacContent)

	configID, err := uploadPAC(pacContent, *secret)
	if err != nil {
		fmt.Println("Failed to upload PAC:", err)
		return
	}

	pacURL := fmt.Sprintf(pacURLFormat, configID)
	fmt.Println("PAC URL:", pacURL)

	header := http.Header{}
	header.Set("X-Secret", *secret)
	client, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		fmt.Println("Failed to connect to websocket server:", err)
		return
	}
	defer client.Close()

	fmt.Println("PAC uploaded successfully. Press Ctrl+C to stop serving.")
	for {
		_, message, err := client.ReadMessage()
		if err != nil {
			fmt.Println("Websocket error, closing connection:", err)
			return
		}
		fmt.Println("Message from server:", string(message))
	}
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return ""
}

func uploadPAC(pacContent string, secret string) (string, error) {
	req, err := http.NewRequest("POST", uploadURL, bytes.NewBufferString(pacContent))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/javascript")
	req.Header.Set("X-Secret", secret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
