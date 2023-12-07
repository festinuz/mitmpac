package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	wifiname "github.com/yelinaung/wifi-name"
)

const (
	uploadURL    = "http://127.0.0.1:8008/upload"
	wsURL        = "ws://127.0.0.1:8008/ws"
	pacURLFormat = "http://127.0.0.1:8008/pac/%s"
)

const (
	configFilename   = ".mitmpac.config"
	defaultProxyPort = "8080"
	pacProxyFormat   = `PROXY %s:%s`
	pacFormat        = `function FindProxyForURL(url, host) {
    return "%s";
}`
)

var defaultProxyPorts = []string{"8888", "8080"}

type FileCOnfig struct {
	Secret string `json:"secret"`
}

type Config struct {
	Secret     string
	LocalIp    string
	ProxyPorts []string
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

func getUserSecret() (secret string, err error) {
	homedir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	configpath := filepath.Join(homedir, configFilename)

	if _, err := os.Stat(configpath); errors.Is(err, os.ErrNotExist) {
		fmt.Println("No config file found: ", configpath)
		newSecret := uuid.New().String()
		fileData := FileCOnfig{
			Secret: newSecret,
		}
		file, err := json.Marshal(fileData)
		if err != nil {
			return "", err
		}
		fmt.Println("Creating new config file in user directory")
		err = os.WriteFile(configpath, file, 0644)
		if err != nil {
			return "", err
		}
	}

	fmt.Println("Reading secret from config file")
	configFile, err := os.Open(configpath)
	if err != nil {
		return "", err
	}
	defer configFile.Close()
	configValue, err := io.ReadAll(configFile)
	if err != nil {
		return "", err
	}

	var fileCOnfig FileCOnfig
	err = json.Unmarshal(configValue, &fileCOnfig)
	if err != nil {
		return "", err
	}

	return fileCOnfig.Secret, nil
}

func getConfig() (config Config, err error) {
	secret, err := getUserSecret()
	if err != nil {
		return config, err
	}
	config.Secret = secret

	ip := getLocalIP()
	if ip == "" {
		return config, errors.New("Failed to get local IP.")
	}
	config.LocalIp = ip

	config.ProxyPorts = defaultProxyPorts

	return config, nil
}

func printNetworkName() {
	networkName := wifiname.WifiName()

	fmt.Printf("Network name: %s\n", networkName)
}

func createPAC(config Config) (PAC string) {
	var proxies []string
	for _, proxyPort := range config.ProxyPorts {
		proxy := fmt.Sprintf(pacProxyFormat, config.LocalIp, proxyPort)
		proxies = append(proxies, proxy)
	}
	proxySetting := strings.Join(proxies, "; ")
	PAC = fmt.Sprintf(pacFormat, proxySetting)
	return PAC
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

func listenForWebsocketUpdates(secret string) {
	headers := http.Header{}
	headers.Set("X-Secret", secret)
	client, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
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

func main() {
	config, err := getConfig()
	if err != nil {
		fmt.Println(err)
		return
	}

	printNetworkName()

	pacContent := createPAC(config)
	fmt.Printf("Generated PAC configuration: \n\n%s\n\n", pacContent)

	configID, err := uploadPAC(pacContent, config.Secret)
	if err != nil {
		fmt.Println("Failed to upload PAC:", err)
		return
	}

	pacURL := fmt.Sprintf(pacURLFormat, configID)
	fmt.Println("PAC URL:", pacURL)

	listenForWebsocketUpdates(config.Secret)
}
