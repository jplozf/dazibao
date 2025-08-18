package main

// ****************************************************************************
// IMPORTS
// ****************************************************************************
import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ****************************************************************************
// TYPES
// ****************************************************************************
// BlockColors defines color settings for a block.
type BlockColors struct {
	Background      string `json:"background,omitempty"`
	TitleColor      string `json:"title_color,omitempty"`
	TitleBackground string `json:"title_background,omitempty"`
	TitleFontSize   string `json:"title_font_size,omitempty"`
	LabelColor      string `json:"label_color,omitempty"`
	LabelBackground string `json:"label_background,omitempty"`
	LabelFontSize   string `json:"label_font_size,omitempty"`
	ValueColor      string `json:"value_color,omitempty"`
	ValueBackground string `json:"value_background,omitempty"`
	ValueFontSize   string `json:"value_font_size,omitempty"`
}

// GlobalColors defines global color settings.
type GlobalColors struct {
	PageBackground string `json:"page_background,omitempty"`
}

// Command represents a single command within a block.
type Command struct {
	Label   string `json:"label"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// Block represents a display block, which can be a single command or a group.
type Block struct {
	Type        string      `json:"type"` // "single" or "group"
	Title       string      `json:"title"`
	Command     string      `json:"command,omitempty"`  // For type "single"
	Commands    []Command   `json:"commands,omitempty"` // For type "group"
	Interval    int         `json:"interval"`
	Output      string      `json:"output,omitempty"` // For type "single"
	LastUpdated time.Time   `json:"last_updated"`
	Colors      BlockColors `json:"colors,omitempty"`
}

// Config represents the application configuration.
type Config struct {
	Blocks      []*Block     `json:"blocks"`
	LastUpdated time.Time    `json:"last_updated"`
	Port        int          `json:"port"`
	Version     string       `json:"version"`
	Colors      GlobalColors `json:"colors,omitempty"`
}

// ****************************************************************************
// VARS
// ****************************************************************************
var (
	config   Config
	mutex    = &sync.Mutex{}
	lockFile *os.File // Global variable to hold the lock file
	version  string   // This will be set by ldflags during build
)

// ****************************************************************************
// CONSTS
// ****************************************************************************
const internalVersion = 0 // Internal version number
const majorVersion = "0"
const appName = "Dazibao"

// ****************************************************************************
// acquireLock()
// ****************************************************************************
func acquireLock() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}
	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	lockFilePath := filepath.Join(dazibaoDir, "dazibao.lock")

	if _, err := os.Stat(dazibaoDir); os.IsNotExist(err) {
		err = os.MkdirAll(dazibaoDir, 0755)
		if err != nil {
			log.Fatalf("Failed to create ~/.dazibao directory: %v", err)
		}
	}

	lockFile, err = os.OpenFile(lockFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			log.Fatalf("Another instance of dazibao is already running. Lock file exists: %s", lockFilePath)
		} else {
			log.Fatalf("Failed to create lock file %s: %v", lockFilePath, err)
		}
	}

	_, err = lockFile.WriteString(fmt.Sprintf("%d", os.Getpid()))
	if err != nil {
		log.Fatalf("Failed to write PID to lock file: %v", err)
	}
	log.Printf("Acquired lock: %s (PID: %d)", lockFilePath, os.Getpid())
}

// ****************************************************************************
// releaseLock()
// ****************************************************************************
func releaseLock() {
	if lockFile != nil {
		lockFilePath := lockFile.Name()
		lockFile.Close()
		err := os.Remove(lockFilePath)
		if err != nil {
			log.Printf("Warning: Failed to remove lock file %s: %v", lockFilePath, err)
		} else {
			log.Printf("Released lock: %s", lockFilePath)
		}
	}
}

// ****************************************************************************
// ensureAssets()
// ****************************************************************************
func ensureAssets() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}
	dazibaoDir := filepath.Join(homeDir, ".dazibao")

	if _, err := os.Stat(dazibaoDir); os.IsNotExist(err) {
		err = os.MkdirAll(dazibaoDir, 0755)
		if err != nil {
			log.Fatalf("Failed to create ~/.dazibao directory: %v", err)
		}
	}

	templatePath := filepath.Join(dazibaoDir, "template.html")
	log.Println("Copying template.html from project root to ~/.dazibao/template.html.")
	srcPath := "template.html"
	srcFile, err := os.ReadFile(srcPath)
	if err != nil {
		log.Fatalf("Failed to read source template.html from %s: %v", srcPath, err)
	}
	err = os.WriteFile(templatePath, srcFile, 0644)
	if err != nil {
		log.Fatalf("Failed to write template.html to %s: %v", templatePath, err)
	}

	iconsSrcDir := "icons"
	iconsDestDir := filepath.Join(dazibaoDir, "icons")
	if _, err := os.Stat(iconsDestDir); os.IsNotExist(err) {
		log.Printf("Copying icons from %s to %s", iconsSrcDir, iconsDestDir)
		err = copyDir(iconsSrcDir, iconsDestDir)
		if err != nil {
			log.Fatalf("Failed to copy icons directory: %v", err)
		}
	}
}

// ****************************************************************************
// main()
// ****************************************************************************
func main() {
	dryRun := flag.Bool("d", false, "Dry run: generate static HTML and exit")
	interval := flag.Int("t", 0, "Interval in seconds for static page generation")
	outputPath := flag.String("o", "", "Optional: Path to write the generated HTML file")
	flag.Parse()

	ensureAssets()

	if *dryRun {
		htmlContent, err := generateAndUpdateStaticHTML()
		if err != nil {
			log.Fatalf("Failed to generate HTML for dry run: %v", err)
		}
		if *outputPath != "" {
			err := writeHTMLToFile(htmlContent, *outputPath)
			if err != nil {
				log.Fatalf("Failed to write HTML to file: %v", err)
			}
			log.Printf("Successfully wrote static page to %s", *outputPath)
		} else {
			fmt.Println(htmlContent)
		}
		os.Exit(0)
	}

	if *interval > 0 {
		executeIntervalGeneration(*interval, *outputPath)
		os.Exit(0)
	}

	startServer()
}

// ****************************************************************************
// writeHTMLToFile()
// ****************************************************************************
func writeHTMLToFile(content, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("could not determine absolute path for %s: %w", path, err)
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create directory %s: %w", dir, err)
	}
	return os.WriteFile(absPath, []byte(content), 0644)
}

// ****************************************************************************
// startServer()
// ****************************************************************************
func startServer() {
	loadConfig()
	config.Version = version

	acquireLock()
	defer releaseLock()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		log.Println("Received termination signal. Releasing lock and exiting...")
		releaseLock()
		os.Exit(0)
	}()

	for _, block := range config.Blocks {
		go runBlock(block)
	}

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/data", dataHandler)
	http.HandleFunc("/icons/dazibao.png", iconHandler)
	log.Printf("dazibao server running on http://localhost:%d. To stop, run: kill %d", config.Port, os.Getpid())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), nil))
}

// ****************************************************************************
// rootHandler()
// ****************************************************************************
func rootHandler(w http.ResponseWriter, r *http.Request) {
	htmlContent, err := generateDynamicHTML()
	if err != nil {
		http.Error(w, "Failed to generate page", http.StatusInternalServerError)
		log.Printf("Error generating HTML for web request: %v", err)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlContent))
}

// ****************************************************************************
// generateDynamicHTML()
// ****************************************************************************
func generateDynamicHTML() (string, error) {
	homeDir, _ := os.UserHomeDir()
	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	templatePath := filepath.Join(dazibaoDir, "template.html")
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to parse template file %s: %w", templatePath, err)
	}

	iconPath := filepath.Join(dazibaoDir, "icons", "dazibao.png")
	iconData, err := os.ReadFile(iconPath)
	var iconDataURI string
	if err != nil {
		log.Printf("Warning: could not read icon file: %v", err)
		iconDataURI = ""
	} else {
		encodedIcon := base64.StdEncoding.EncodeToString(iconData)
		iconDataURI = "data:image/png;base64," + encodedIcon
	}

	templateData := struct {
		ConfigJSON  template.JS
		IconDataURI template.URL
	}{
		ConfigJSON:  template.JS("null"),
		IconDataURI: template.URL(iconDataURI),
	}

	var renderedHTML bytes.Buffer
	err = tmpl.Execute(&renderedHTML, templateData)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return renderedHTML.String(), nil
}

// ****************************************************************************
// executeIntervalGeneration()
// ****************************************************************************
func executeIntervalGeneration(interval int, outputPath string) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Starting static page generation every %d seconds. Press Ctrl+C to stop.", interval)

	runGeneration := func() {
		log.Println("Generating static page...")
		htmlContent, err := generateAndUpdateStaticHTML()
		if err != nil {
			log.Printf("Error generating static page: %v", err)
			return
		}

		finalPath := outputPath
		if finalPath == "" {
			homeDir, _ := os.UserHomeDir()
			finalPath = filepath.Join(homeDir, ".dazibao", "index.html")
		}

		err = writeHTMLToFile(htmlContent, finalPath)
		if err != nil {
			log.Printf("Error writing to %s: %v", finalPath, err)
		} else {
			absPath, _ := filepath.Abs(finalPath)
			log.Printf("Successfully updated %s", absPath)
		}
	}

	runGeneration() // Run once immediately

	for {
		select {
		case <-ticker.C:
			runGeneration()
		case <-signals:
			log.Println("Received termination signal. Exiting...")
			return
		}
	}
}

// ****************************************************************************
// generateHTML()
// ****************************************************************************
func generateHTML(cfg Config) (string, error) {
	homeDir, _ := os.UserHomeDir()
	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	templatePath := filepath.Join(dazibaoDir, "template.html")
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to parse template file %s: %w", templatePath, err)
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config to JSON: %w", err)
	}

	iconPath := filepath.Join(dazibaoDir, "icons", "dazibao.png")
	iconData, err := os.ReadFile(iconPath)
	var iconDataURI string
	if err != nil {
		log.Printf("Warning: could not read icon file: %v", err)
		iconDataURI = ""
	} else {
		encodedIcon := base64.StdEncoding.EncodeToString(iconData)
		iconDataURI = "data:image/png;base64," + encodedIcon
	}

	templateData := struct {
		ConfigJSON  template.JS
		IconDataURI template.URL
	}{
		ConfigJSON:  template.JS(configJSON),
		IconDataURI: template.URL(iconDataURI),
	}

	var renderedHTML bytes.Buffer
	err = tmpl.Execute(&renderedHTML, templateData)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return renderedHTML.String(), nil
}

// ****************************************************************************
// generateAndUpdateStaticHTML()
// ****************************************************************************
func generateAndUpdateStaticHTML() (string, error) {
	cfg, err := getFreshConfig()
	if err != nil {
		return "", fmt.Errorf("could not load config: %w", err)
	}
	cfg.Version = version

	for _, block := range cfg.Blocks {
		switch block.Type {
		case "single":
			output, err := executeCommandOrVariable(block.Command)
			if err != nil {
				block.Output = fmt.Sprintf("Error: %v", err)
			} else {
				block.Output = output
			}
		case "group":
			for i := range block.Commands {
				output, err := executeCommandOrVariable(block.Commands[i].Command)
				if err != nil {
					block.Commands[i].Output = fmt.Sprintf("Error: %v", err)
				} else {
					block.Commands[i].Output = output
				}
			}
		}
		block.LastUpdated = time.Now()
	}
	cfg.LastUpdated = time.Now()

	err = saveConfigToFile(cfg)
	if err != nil {
		return "", fmt.Errorf("could not save updated config: %w", err)
	}

	return generateHTML(cfg)
}

// ****************************************************************************
// getFreshConfig()
// ****************************************************************************
func getFreshConfig() (Config, error) {
	var freshConfig Config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return freshConfig, fmt.Errorf("failed to get user home directory: %w", err)
	}
	configFilePath := filepath.Join(homeDir, ".dazibao", "config.json")

	file, err := os.ReadFile(configFilePath)
	if err != nil {
		return freshConfig, fmt.Errorf("failed to read config file: %w", err)
	}

	err = json.Unmarshal(file, &freshConfig)
	if err != nil {
		return freshConfig, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if freshConfig.Port == 0 {
		freshConfig.Port = 8080
	}
	return freshConfig, nil
}

// ****************************************************************************
// createDefaultConfig()
// ****************************************************************************
func createDefaultConfig() Config {
	return Config{
		Blocks: []*Block{
			{
				Type:     "single",
				Title:    "Uptime",
				Command:  "uptime",
				Interval: 5,
				Colors:   BlockColors{Background: "#fff", TitleColor: "#333", TitleBackground: "#eee", TitleFontSize: "1.2em", ValueFontSize: "1em"},
			},
			{
				Type:     "single",
				Title:    "Disk Usage",
				Command:  "df -h",
				Interval: 10,
				Colors:   BlockColors{Background: "#fff", TitleColor: "#333", TitleBackground: "#eee", TitleFontSize: "1.2em", ValueFontSize: "1em"},
			},
			{
				Type:  "group",
				Title: "System Info",
				Commands: []Command{
					{Label: "Hostname", Command: "%hostname"},
					{Label: "Current Time", Command: "%time"},
					{Label: "Current Date", Command: "%date"},
					{Label: "Username", Command: "%username"},
					{Label: "IP Address", Command: "%ip_address"},
				},
				Interval: 5,
				Colors:   BlockColors{Background: "#f9f9f9", TitleColor: "#0056b3", TitleBackground: "#e0f2f7", TitleFontSize: "1.2em", LabelColor: "#555", LabelBackground: "#f0f0f0", LabelFontSize: "1em", ValueColor: "#222", ValueBackground: "#fff", ValueFontSize: "1em"},
			},
		},
		LastUpdated: time.Now(),
		Port:        8080,
		Colors:      GlobalColors{PageBackground: "#f0f0f0"},
	}
}

// ****************************************************************************
// loadConfig()
// ****************************************************************************
func loadConfig() {
	cfg, err := getFreshConfig()
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file or directory") {
			log.Println("~/.dazibao/config.json not found, creating with default blocks.")
			config = createDefaultConfig()
			err = saveConfigToFile(config)
			if err != nil {
				log.Fatalf("Failed to save initial default config: %v", err)
			}
			return
		}
		homeDir, _ := os.UserHomeDir()
		configFilePath := filepath.Join(homeDir, ".dazibao", "config.json")
		log.Fatalf("Failed to load config file %s: %v", configFilePath, err)
	}
	config = cfg

	// DEBUG: Log the loaded config path and content
	homeDirDebug, errDebug := os.UserHomeDir()
	if errDebug != nil {
		log.Printf("Error getting home directory for debug log: %v", errDebug)
		return
	}
	log.Printf("Loaded config from: %s", filepath.Join(homeDirDebug, ".dazibao", "config.json"))
	configJSON, _ := json.MarshalIndent(config, "", "  ")
	log.Printf("Loaded config content:\n%s", string(configJSON))
}

// ****************************************************************************
// saveConfigToFile()
// ****************************************************************************
func saveConfigToFile(cfg Config) error {
	mutex.Lock()
	defer mutex.Unlock()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("error getting user home directory: %w", err)
	}
	configFilePath := filepath.Join(homeDir, ".dazibao", "config.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshalling config: %w", err)
	}

	err = os.WriteFile(configFilePath, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing config file %s: %w", configFilePath, err)
	}
	return nil
}

// ****************************************************************************
// saveConfig()
// ****************************************************************************
func saveConfig() {
	if err := saveConfigToFile(config); err != nil {
		log.Printf("Error saving config: %v", err)
	}
}

// ****************************************************************************
// runBlock()
// ****************************************************************************
func runBlock(block *Block) {
	ticker := time.NewTicker(time.Duration(block.Interval) * time.Second)
	for ; true; <-ticker.C {
		mutex.Lock()
		switch block.Type {
		case "single":
			output, err := executeCommandOrVariable(block.Command)
			if err != nil {
				log.Printf("Error executing command for block '%s' (command: %s): %v", block.Title, block.Command, err)
				block.Output = fmt.Sprintf("Error: %v", err)
			} else {
				block.Output = output
			}
		case "group":
			for i := range block.Commands {
				output, err := executeCommandOrVariable(block.Commands[i].Command)
				if err != nil {
					log.Printf("Error executing command '%s' in group '%s': %v", block.Commands[i].Label, block.Title, err)
					block.Commands[i].Output = fmt.Sprintf("Error: %v", err)
				} else {
					block.Commands[i].Output = output
				}
			}
		}
		block.LastUpdated = time.Now()
		config.LastUpdated = time.Now()
		mutex.Unlock()
	}
}

// ****************************************************************************
// executeCommandOrVariable()
// ****************************************************************************
func executeCommandOrVariable(cmdStr string) (string, error) {
	if len(cmdStr) > 1 && cmdStr[0] == '%' {
		return resolveVariable(cmdStr), nil
	} else {
		out, err := exec.Command("bash", "-c", cmdStr).CombinedOutput()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// ****************************************************************************
// resolveVariable()
// ****************************************************************************
func resolveVariable(variable string) string {
	switch variable {
	case "%hostname":
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return hostname
	case "%time":
		return time.Now().Format("15:04:05")
	case "%date":
		return time.Now().Format("2006-01-02")
	case "%year":
		return time.Now().Format("2006")
	case "%month":
		return time.Now().Format("01")
	case "%day":
		return time.Now().Format("02")
	case "%dayname":
		return time.Now().Format("Monday")
	case "%hours":
		return time.Now().Format("15")
	case "%minutes":
		return time.Now().Format("04")
	case "%seconds":
		return time.Now().Format("05")
	case "%username":
		user, err := user.Current()
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return user.Username
	case "%ip_address":
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
		return "N/A"
	case "%app_name":
		return appName
	case "%app_version":
		return version
	default:
		return "Unknown variable"
	}
}

// ****************************************************************************
// copyDir()
// ****************************************************************************
func copyDir(src string, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	err = os.MkdirAll(dest, srcInfo.Mode())
	if err != nil {
		return err
	}

	dir, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range dir {
		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			err = copyDir(srcPath, destPath)
			if err != nil {
				return err
			}
		} else {
			err = copyFile(srcPath, destPath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ****************************************************************************
// copyFile()
// ****************************************************************************
func copyFile(src, dest string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	err = os.WriteFile(dest, input, 0644)
	if err != nil {
		return err
	}
	return nil
}

// ****************************************************************************
// iconHandler()
// ****************************************************************************
func iconHandler(w http.ResponseWriter, r *http.Request) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Failed to get user home directory: %v", err)
		return
	}
	iconPath := filepath.Join(homeDir, ".dazibao", "icons", "dazibao.png")

	if _, err := os.Stat(iconPath); os.IsNotExist(err) {
		http.Error(w, "Icon not found", http.StatusNotFound)
		log.Printf("Icon file not found: %s", iconPath)
		return
	}

	http.ServeFile(w, r, iconPath)
}

// ****************************************************************************
// dataHandler()
// ****************************************************************************
func dataHandler(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	// DEBUG: Log the config content before sending to frontend
	configJSON, _ := json.MarshalIndent(config, "", "  ")
	log.Printf("Sending config to frontend:\n%s", string(configJSON))
	json.NewEncoder(w).Encode(config)
}
