package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os" // Need os for pty file handle
	"os/exec"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// "time" // Keep if needed for delays or resize polling

	// Import the PTY library
	"github.com/creack/pty"
)

// Command to execute - USE HTOP FOR TESTING THIS FIX
var (
	appDir         string
	fileLocation         = "files/"
	outputLocation       = "output/"
	taskCounter    int32 = 0
	taskMap        Map[string, *taskInfo]
	installedLangs []string
)

type taskInfo struct {
	filename string
	filepath string
	language []string
	deskew   bool
	rotate   bool
	force    bool
	result   string
	status   int32
	expiry   time.Time
}

type Resp struct {
	TaskId string `json:"taskId"`
}

func main() {
	appDir = path.Dir(os.Args[0])
	fileLocation = path.Join(appDir, fileLocation)
	outputLocation = path.Join(appDir, outputLocation)

	// make sure these folders exist
	os.MkdirAll(fileLocation, 0755)
	os.MkdirAll(outputLocation, 0755)

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/upload", taskByUpload)
	http.HandleFunc("/runTask", runTask)
	http.HandleFunc("/status", taskStatus)
	http.HandleFunc("/retrieve", downloadAsset)
	http.HandleFunc("/langlist", langList)

	fmt.Println("Server started on http://0.0.0.0:8080")
	installedLangs, _ = GetInstalledTesseractLanguages()

	go taskCleaner()
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, appDir+"/index.html")
}

func langList(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(installedLangs)
}

func taskStatus(w http.ResponseWriter, r *http.Request) {
	taskId := r.URL.Query().Get("taskId")
	if taskId == "" {
		http.NotFound(w, r)
		return
	}

	task, ok := taskMap.Load(taskId)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(fmt.Sprintf("{\"status\": %d}", task.status)))
}

func newTaskId() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddInt32(&taskCounter, 1))
}

func taskByUpload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(32 << 20)
	f, _, err := r.FormFile("file")
	filename := sanitizeFilename(r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if filename == "" {
		http.Error(w, "invalid filename", http.StatusInternalServerError)
		return
	}
	taskId := newTaskId()
	diskfile, err := os.Create(path.Join(fileLocation, taskId))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer diskfile.Close()
	if _, err := io.Copy(diskfile, f); err != nil && err != io.EOF {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// parse language list
	lang, _ := r.MultipartForm.Value["language"]
	for i, l := range lang {
		lang[i] = strings.ToLower(l) // keep only the 3 left-most letters
	}

	taskMap.Store(taskId, &taskInfo{
		filename: filename,
		filepath: path.Join(fileLocation, taskId),
		status:   0,
		language: lang,
		deskew:   r.FormValue("deskew") == "on",
		rotate:   r.FormValue("rotate") == "on",
		force:    r.FormValue("force") == "on",
		result:   path.Join(outputLocation, "ocr_"+filename),
		expiry:   time.Now().Add(time.Minute * 30),
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := Resp{
		TaskId: taskId,
	}
	encoder := json.NewEncoder(w)
	encoder.Encode(resp)
}

func taskCleaner() {
	for {
		time.Sleep(time.Minute)
		removeKeys := make([]string, 0)
		t := time.Now()
		taskMap.Range(func(key string, value *taskInfo) bool {
			if value.expiry.Before(t) {
				if value.filename != "" {
					os.Remove(value.filepath)
				}
				if value.result != "" {
					os.Remove(value.result)
				}
				removeKeys = append(removeKeys, key)
			}
			return true
		})
		for _, key := range removeKeys {
			taskMap.Delete(key)
		}
	}
}

func downloadAsset(w http.ResponseWriter, r *http.Request) {
	taskId := r.URL.Query().Get("taskId")

	task, ok := taskMap.Load(taskId)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if task.status != 2 || task.result == "" {
		http.Error(w, "task not finished", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"ocr_%s\"", task.filename))
	http.ServeFile(w, r, task.result)
	task.expiry = time.Now().Add(time.Minute * 3) // downloaded task will have an expiry of 3m
}

// GetInstalledTesseractLanguages runs the tesseract --list-langs command
// and returns a slice of available language codes.
// It returns an error if the command fails or tesseract is not found.
func GetInstalledTesseractLanguages() ([]string, error) {
	// Define the command to execute
	cmd := exec.Command("tesseract", "--list-langs")
	// Create a buffer to capture the standard output
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		// Return a specific error if the command couldn't be executed
		// (e.g., tesseract not found in PATH)
		return nil, fmt.Errorf("failed to run tesseract command: %w", err)
	}
	// Process the captured output.
	// The output typically looks like:
	// List of available languages in /path/to/tessdata/:
	// eng
	// fra
	// ...
	outputStr := out.String()
	lines := strings.Split(outputStr, "\n")
	languages := []string{}
	// Skip the first line (the header) and process the rest
	// Ensure there's at least one line (the header)
	if len(lines) > 0 {
		// Start processing from the second line
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line) // Remove potential leading/trailing whitespace
			if line != "" {                // Ignore empty lines
				languages = append(languages, line)
			}
		}
	} else {
		// This case is unlikely if tesseract ran successfully, but good for robustness
		return nil, fmt.Errorf("tesseract --list-langs returned empty output")
	}
	return languages, nil
}

func runTask(w http.ResponseWriter, r *http.Request) {
	taskId := r.URL.Query().Get("taskId")

	task, ok := taskMap.Load(taskId)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if task.status != 0 && task.status != 3 {
		http.Error(w, "task already running", http.StatusInternalServerError)
		return
	}
	taskResult := 3
	defer func() {
		task.status = int32(taskResult)
		task.expiry = time.Now().Add(time.Minute * 10) // completed task will be removed in 10m
		os.Remove(task.filepath)                       // remove temp file immediately
	}()

	log.Printf("Task %s connected, attempting PTY start...\n", taskId)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Println("Streaming unsupported!")
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	task.status = 1
	// construct our command
	commandName := "ocrmypdf"
	commandArgs := []string{}
	if len(task.language) > 0 {
		commandArgs = append(commandArgs, "-l", strings.Join(task.language, "+"))
	}
	if task.deskew {
		commandArgs = append(commandArgs, "--deskew")
	}
	if task.rotate {
		commandArgs = append(commandArgs, "--rotate-pages")
	}
	if task.force {
		commandArgs = append(commandArgs, "--force-ocr")
	}
	commandArgs = append(commandArgs, task.filepath, task.result)

	// Prepare the command using exec.Command as before
	cmd := exec.Command(commandName, commandArgs...)
	// Set TERM - still important for htop rendering
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// --- Start the command with a PTY ---
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("Error starting PTY: %v", err)
		http.Error(w, fmt.Sprintf("Error starting PTY: %v", err), http.StatusInternalServerError)
		return
	}
	// Make sure to close the PTY master side when the function exits
	defer func() {
		log.Println("Closing PTY master...")
		_ = ptmx.Close()
		log.Println("PTY master closed.")
	}()

	// --- Set initial terminal size ---
	// htop needs this! Start with a common default.
	// For a real application, you'd want the frontend to send its initial size.
	initialWinSize := &pty.Winsize{Rows: 24, Cols: 80}
	if err := pty.Setsize(ptmx, initialWinSize); err != nil {
		log.Printf("Warning: failed to set initial PTY size: %v", err)
		// Continue anyway, htop might cope or use a default
	} else {
		log.Printf("Set initial PTY size to %dx%d", initialWinSize.Cols, initialWinSize.Rows)
	}

	var wg sync.WaitGroup
	// The PTY provides a single file descriptor for combined stdout/stderr.
	// So we only need one reader goroutine.
	wg.Add(1)

	// Use a channel to signal when reading is done.
	ptyClosedChan := make(chan struct{})

	// Start goroutine to read from the PTY master
	go streamPipe("pty", ptmx, w, flusher, &wg, ptyClosedChan)

	// Goroutine to wait for the command to exit (different from waiting on pipes)
	// We also need to wait for the reader goroutine to finish processing all output.
	processExitChan := make(chan struct{})
	go func() {
		defer close(processExitChan) // Signal when this goroutine finishes
		// Wait for the underlying command process to finish
		err := cmd.Wait()
		if err != nil {
			// Log the exit error of the command itself
			// This might be non-zero if htop is killed or errors internally
			log.Printf("Command process finished with error: %v", err)
		} else {
			taskResult = 2
			log.Println("Command process finished successfully.")
		}
		// Wait for the streamPipe goroutine to definitely finish reading
		// It will close ptyClosedChan when it gets EOF or an error
		log.Println("Waiting for PTY reader to finish...")
		<-ptyClosedChan // Wait until the reader signals it's done
		log.Println("PTY reader finished signal received.")
	}()

	// Keep connection open: wait for the process to exit AND reader to finish,
	// OR for the client to disconnect.
	select {
	case <-processExitChan:
		log.Println("Command process exited and PTY reader finished normally.")
		// All output should have been streamed by now.
	case <-r.Context().Done():
		log.Println("Client disconnected.")
		// Client left. We should try to kill the process cleanly.
		// Closing the PTY master (`defer ptmx.Close()`) often sends SIGHUP,
		// which might be enough for htop to exit. Explicit kill is safer.
		if cmd.Process != nil {
			log.Println("Attempting to kill process due to client disconnect...")
			err := cmd.Process.Kill()
			if err != nil {
				log.Printf("Failed to kill process: %v", err)
			} else {
				log.Println("Killed process.")
			}
		}
		// Wait for the reader/waiter goroutines to finish after kill/close
		// Adding a small timeout here might prevent hangs if cleanup fails
		// select {
		// case <-processExitChan:
		// 	log.Println("Cleanup finished after client disconnect.")
		// case <-time.After(2 * time.Second):
		// 	log.Println("Timeout waiting for cleanup after client disconnect.")
		// }
	}

	// Ensure the PTY master is closed before handler fully exits (defer helps here)
	log.Println("Exiting stream handler.")
}

// streamPipe modified slightly to signal when it's done via a channel
func streamPipe(streamName string, pipe io.ReadCloser, w http.ResponseWriter, flusher http.Flusher, wg *sync.WaitGroup, doneChan chan<- struct{}) {
	defer func() {
		wg.Done()
		close(doneChan) // Signal that this reader has finished
		log.Printf("Finished reading from %s and closed doneChan", streamName)
	}()

	// Increased buffer size might be beneficial for chatty interactive apps
	buffer := make([]byte, 4096)

	for {
		n, err := pipe.Read(buffer) // Read raw bytes from PTY master
		if n > 0 {
			b64Data := base64.StdEncoding.EncodeToString(buffer[:n])
			jsonData := fmt.Sprintf("{\"type\": \"output\", \"stream\": \"%s\", \"data_b64\": %q}", streamName, b64Data)

			_, writeErr := fmt.Fprintf(w, "data: %s\n\n", jsonData)
			if writeErr != nil {
				log.Printf("Error writing to client (%s): %v. Stopping stream.", streamName, writeErr)
				// Don't just break, return to trigger the defer cleanup & doneChan close
				return
			}
			flusher.Flush()
		}

		if err != nil {
			if err != io.EOF {
				// Log read errors. These can happen, e.g., if PTY is closed forcefully.
				log.Printf("Error reading %s: %v", streamName, err)
			} else {
				// EOF is expected when the process attached to the PTY exits
				log.Printf("%s stream reached EOF.", streamName)
			}
			// Exit loop on EOF or any other error
			break
		}
	}
}

func sanitizeFilename(input string) string {
	invalidChars := regexp.MustCompile(`[<>:"/\\|?*]`)
	// Replace invalid characters with an underscore `_`.
	sanitized := invalidChars.ReplaceAllString(input, "_")
	// Trim any leading or trailing whitespace to clean up the result.
	sanitized = strings.TrimSpace(sanitized)
	// Optional: Truncate the filename if it's too long (e.g., limit to 255 characters).
	// Most filesystems (like ext4 and NTFS) have a 255-character limit for filenames.
	if len(sanitized) > 255 {
		sanitized = sanitized[:255]
	}
	return sanitized
}
