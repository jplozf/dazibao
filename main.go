package main

// ****************************************************************************
// IMPORTS
// ****************************************************************************
import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ****************************************************************************
// TYPES
// ****************************************************************************
// Command represents a command to be executed.
type Command struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Interval int    `json:"interval"`
	Output   string `json:"output"`
}

// Config represents the application configuration.
type Config struct {
	Commands    []Command `json:"commands"`
	LastUpdated time.Time `json:"last_updated"`
	Port        int       `json:"port"`
	Version     string    `json:"version"`
}

// ****************************************************************************
// VARS
// ****************************************************************************
var (
	config   Config
	mutex    = &sync.Mutex{}
	lockFile *os.File // Global variable to hold the lock file
)

// ****************************************************************************
// CONSTS
// ****************************************************************************
const internalVersion = 0 // Internal version number

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
// main()
// ****************************************************************************
func main() {
	// Acquire application lock
	acquireLock()
	defer releaseLock()

	// Load configuration
	loadConfig()

	// Get Git commit count and hash
	commitCountBytes, err := exec.Command("git", "rev-list", "--count", "HEAD").Output()
	if err != nil {
		log.Printf("Warning: Could not get Git commit count: %v", err)
		config.Version = fmt.Sprintf("%d.0-unknown", internalVersion)
	} else {
		commitCount := "0"
		fmt.Sscanf(string(commitCountBytes), "%s", &commitCount)

		commitHashBytes, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
		if err != nil {
			log.Printf("Warning: Could not get Git commit hash: %v", err)
			config.Version = fmt.Sprintf("%d.%s-unknown", internalVersion, commitCount)
		} else {
			commitHash := "unknown"
			fmt.Sscanf(string(commitHashBytes), "%s", &commitHash)
			config.Version = fmt.Sprintf("%d.%s-%s", internalVersion, commitCount, commitHash)
		}
	}

	// Ensure ~/.dazibao directory exists
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

	// Start command runners
	for i := range config.Commands {
		go runCommand(&config.Commands[i])
	}

	// Serve static files from ~/.dazibao
	http.Handle("/", http.FileServer(http.Dir(dazibaoDir)))
	http.HandleFunc("/data", dataHandler)
	log.Printf("dazibao server running on http://localhost:%d. To stop, run: kill %d", config.Port, os.Getpid())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), nil))
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
			log.Println("~/.dazibao/config.json not found, creating with default commands.")
			config = Config{
				Commands: []Command{
					{Name: "Uptime", Command: "uptime", Interval: 5, Output: ""},
					{Name: "Disk Usage", Command: "df -h", Interval: 10, Output: ""},
					{Name: "Memory Usage", Command: "free -h", Interval: 5, Output: ""},
				},
				LastUpdated: time.Now(),
				Port:        8080, // Default port
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
// runCommand()
// ****************************************************************************
func runCommand(cmd *Command) {
	ticker := time.NewTicker(time.Duration(cmd.Interval) * time.Second)
	for ; true; <-ticker.C {
		out, err := exec.Command("bash", "-c", cmd.Command).CombinedOutput()
		mutex.Lock() // Lock before modifying cmd.Output
		if err != nil {
			log.Printf("Error executing command '%s': %v", cmd.Name, err)
			cmd.Output = fmt.Sprintf("Error: %v", err)
		} else {
			cmd.Output = string(out)
		}
		// log.Printf("Command '%s' output updated. New output length: %d", cmd.Name, len(cmd.Output)) // Removed frequent log
		mutex.Unlock() // Unlock after modifying cmd.Output

		saveConfig() // Save the updated config after each command execution
	}
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
