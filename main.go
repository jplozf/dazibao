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
	LabelColor      string `json:"label_color,omitempty"`
	LabelBackground string `json:"label_background,omitempty"`
	ValueColor      string `json:"value_color,omitempty"`
	ValueBackground string `json:"value_background,omitempty"`
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

	// Create ~/.dazibao directory if it doesn't exist
	if _, err := os.Stat(dazibaoDir); os.IsNotExist(err) {
		err = os.MkdirAll(dazibaoDir, 0755)
		if err != nil {
			log.Fatalf("Failed to create ~/.dazibao directory: %v", err)
		}
	}

	// Try to create and lock the file
	lockFile, err = os.OpenFile(lockFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			log.Fatalf("Another instance of dazibao is already running. Lock file exists: %s", lockFilePath)
		} else {
			log.Fatalf("Failed to create lock file %s: %v", lockFilePath, err)
		}
	}

	// Write PID to lock file
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

	// Create ~/.dazibao directory if it doesn't exist
	if _, err := os.Stat(dazibaoDir); os.IsNotExist(err) {
		err = os.MkdirAll(dazibaoDir, 0755)
		if err != nil {
			log.Fatalf("Failed to create ~/.dazibao directory: %v", err)
		}
	}

	// Create ~/.dazibao/index.html if it doesn't exist
	indexPath := filepath.Join(dazibaoDir, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		log.Println("~/.dazibao/index.html not found, copying from project root.")
		srcPath := "index.html" // Path to index.html in the project root
		srcFile, err := os.ReadFile(srcPath)
		if err != nil {
			log.Fatalf("Failed to read source index.html from %s: %v", srcPath, err)
		}
		err = os.WriteFile(indexPath, srcFile, 0644)
		if err != nil {
			log.Fatalf("Failed to write index.html to %s: %v", indexPath, err)
		}
	}

	// Copy icons directory to ~/.dazibao if it doesn't exist
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
	// Define a 'dry-run' flag
	dryRun := flag.Bool("d", false, "Dry run: generate static HTML and exit")
	flag.Parse()

	// Load configuration
	loadConfig()
	config.Version = version

	// Ensure assets are in place before doing anything else
	ensureAssets()

	if *dryRun {
		// Execute dry run logic
		executeDryRun()
		os.Exit(0)
	}

	// Acquire application lock
	acquireLock()
	defer releaseLock()

	// Set up signal handling
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		log.Println("Received termination signal. Releasing lock and exiting...")
		releaseLock()
		os.Exit(0)
	}()

	// Start block runners
	for _, block := range config.Blocks {
		go runBlock(block)
	}

	// Serve static files from ~/.dazibao
	homeDir, _ := os.UserHomeDir()
	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	http.Handle("/", http.FileServer(http.Dir(dazibaoDir)))
	http.HandleFunc("/data", dataHandler)
	http.HandleFunc("/icons/dazibao.png", iconHandler)
	log.Printf("dazibao server running on http://localhost:%d. To stop, run: kill %d", config.Port, os.Getpid())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), nil))
}

// ****************************************************************************
// executeDryRun()
// ****************************************************************************
func executeDryRun() {
	log.Println("Executing dry run...")

	// 1. Execute all commands once
	for _, block := range config.Blocks {
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
	config.LastUpdated = time.Now()

	// 2. Load and parse the HTML template
	homeDir, _ := os.UserHomeDir()
	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	templatePath := filepath.Join(dazibaoDir, "index.html")
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		log.Fatalf("Failed to parse template file %s: %v", templatePath, err)
	}

	// 3. Marshal the config to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		log.Fatalf("Failed to marshal config to JSON: %v", err)
	}

	// 4. Read and encode the icon
	iconPath := filepath.Join(dazibaoDir, "icons", "dazibao.png")
	iconData, err := os.ReadFile(iconPath)
	var iconDataURI string
	if err != nil {
		log.Printf("Warning: could not read icon file for dry run: %v", err)
		iconDataURI = "" // Will use fallback in template
	} else {
		encodedIcon := base64.StdEncoding.EncodeToString(iconData)
		iconDataURI = "data:image/png;base64," + encodedIcon
	}

	// 5. Create a data structure for the template
	templateData := struct {
		ConfigJSON  template.JS
		IconDataURI template.URL
	}{
		ConfigJSON:  template.JS(configJSON),
		IconDataURI: template.URL(iconDataURI),
	}

	// 6. Execute the template and write to a buffer
	var renderedHTML bytes.Buffer
	err = tmpl.Execute(&renderedHTML, templateData)
	if err != nil {
		log.Fatalf("Failed to execute template: %v", err)
	}

	// 7. Print the final HTML to stdout
	fmt.Println(renderedHTML.String())
}

// ****************************************************************************
// loadConfig()
// ****************************************************************************
func loadConfig() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	dazibaoDir := filepath.Join(homeDir, ".dazibao")
	configFilePath := filepath.Join(dazibaoDir, "config.json")

	// Create ~/.dazibao directory if it doesn't exist
	if _, err := os.Stat(dazibaoDir); os.IsNotExist(err) {
		err = os.MkdirAll(dazibaoDir, 0755)
		if err != nil {
			log.Fatalf("Failed to create ~/.dazibao directory: %v", err)
		}
	}

	file, err := os.ReadFile(configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("~/.dazibao/config.json not found, creating with default blocks.")
			config = Config{
				Blocks: []*Block{
					{
						Type:     "single",
						Title:    "Uptime",
						Command:  "uptime",
						Interval: 5,
						Colors: BlockColors{
							Background:      "#fff",
							TitleColor:      "#333",
							TitleBackground: "#eee",
						},
					},
					{
						Type:     "single",
						Title:    "Disk Usage",
						Command:  "df -h",
						Interval: 10,
						Colors: BlockColors{
							Background:      "#fff",
							TitleColor:      "#333",
							TitleBackground: "#eee",
						},
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
						Colors: BlockColors{
							Background:      "#f9f9f9",
							TitleColor:      "#0056b3",
							TitleBackground: "#e0f2f7",
							LabelColor:      "#555",
							LabelBackground: "#f0f0f0",
							ValueColor:      "#222",
							ValueBackground: "#fff",
						},
					},
				},
				LastUpdated: time.Now(),
				Port:        8080, // Default port
				Colors: GlobalColors{
					PageBackground: "#f0f0f0",
				},
			}
			saveConfig() // Save the newly created default config
			return
		}
		log.Fatalf("Failed to read config file: %v", err)
	}

	err = json.Unmarshal(file, &config)
	if err != nil {
		log.Fatalf("Failed to unmarshal config: %v", err)
	}

	// If port is 0 after unmarshaling (e.g., not in config file), set default
	if config.Port == 0 {
		config.Port = 8080
	}
}

// ****************************************************************************
// saveConfig()
// ****************************************************************************
func saveConfig() {
	mutex.Lock()
	defer mutex.Unlock()

	config.LastUpdated = time.Now() // Update timestamp before saving

	// log.Println("Attempting to save config...") // Removed frequent log
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Error getting user home directory for saving config: %v", err)
		return
	}
	configFilePath := filepath.Join(homeDir, ".dazibao", "config.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Error marshalling config: %v", err)
		return
	}

	err = os.WriteFile(configFilePath, data, 0644)
	if err != nil {
		log.Printf("Error writing config file %s: %v", configFilePath, err)
	} else {
		// log.Printf("Config saved successfully to %s", configFilePath) // Removed frequent log
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
				log.Printf("Error executing command for block '%s': %v", block.Title, err)
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
		mutex.Unlock()
		saveConfig()
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
		return string(out), nil
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
		// This is a simplified way to get an IP address, might not be robust for all cases
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

	// Check if the icon file exists
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

	// log.Printf("Serving full config: %+v", config) // Removed frequent log
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}
