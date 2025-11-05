package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"sync"

	"github.com/google/uuid"
)

// Session 表示一个 PowerShell 会话
type Session struct {
	ID      string
	Cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	Stderr  io.ReadCloser
	Running bool
	mu      sync.Mutex
}

// SessionManager 管理所有会话
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession 创建新的 PowerShell 会话
func (sm *SessionManager) CreateSession() (*Session, error) {
	sessionID := uuid.New().String()

	// -NoProfile: 不加载 PowerShell 配置文件
	// -NoLogo: 不显示版权信息
	// -NoExit: 执行命令后不退出
	// 设置所有编码为 UTF-8 以避免中文乱码
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NoLogo", "-NoExit", "-InputFormat", "Text", "-OutputFormat", "Text", "-Command", "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; [Console]::InputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start powershell: %v", err)
	}

	session := &Session{
		ID:      sessionID,
		Cmd:     cmd,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		Running: true,
	}

	sm.mu.Lock()
	sm.sessions[sessionID] = session
	sm.mu.Unlock()

	log.Printf("Created session: %s", sessionID)
	return session, nil
}

// GetSession 获取指定的会话
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, exists := sm.sessions[sessionID]
	return session, exists
}

// EndSession 结束指定的会话
func (sm *SessionManager) EndSession(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.Running {
		session.Stdin.Close()
		session.Cmd.Process.Kill()
		session.Running = false
	}

	delete(sm.sessions, sessionID)
	log.Printf("Ended session: %s", sessionID)
	return nil
}

// RunCommand 在指定会话中执行命令
func (s *Session) RunCommand(command string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.Running {
		return "", fmt.Errorf("session is not running")
	}

	// 使用唯一标记来分隔输出
	marker := uuid.New().String()
	// 使用 *>&1 将所有输出流(包括错误)重定向到标准输出
	fullCommand := fmt.Sprintf("& { %s } *>&1 | Out-String; Write-Host '%s'\n", command, marker)

	// 写入命令
	if _, err := s.Stdin.Write([]byte(fullCommand)); err != nil {
		return "", fmt.Errorf("failed to write command: %v", err)
	}

	// 读取输出直到遇到标记
	output := make([]byte, 0, 4096)
	buffer := make([]byte, 1024)
	markerBytes := []byte(marker)

	for {
		n, err := s.Stdout.Read(buffer)
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("failed to read output: %v", err)
		}

		if n > 0 {
			output = append(output, buffer[:n]...)

			// 检查是否包含标记
			if len(output) >= len(markerBytes) {
				// 在输出中查找标记
				for i := len(output) - n; i <= len(output)-len(markerBytes); i++ {
					if string(output[i:i+len(markerBytes)]) == marker {
						// 找到标记,返回标记之前的内容
						result := string(output[:i])
						// 清理剩余的换行符
						if len(result) > 0 && result[len(result)-1] == '\n' {
							result = result[:len(result)-1]
						}
						if len(result) > 0 && result[len(result)-1] == '\r' {
							result = result[:len(result)-1]
						}
						return result, nil
					}
				}
			}
		}

		// 避免无限等待
		if len(output) > 1024*1024 { // 1MB 限制
			break
		}
	}

	return string(output), nil
}

var sessionManager *SessionManager

// API1: 开启新会话
func handleStartSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, err := sessionManager.CreateSession()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create session: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"session_id": session.ID,
	})
}

// API2: 执行命令
func handleRunCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.Command == "" {
		http.Error(w, "session_id and command are required", http.StatusBadRequest)
		return
	}

	session, exists := sessionManager.GetSession(req.SessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	output, err := session.RunCommand(req.Command)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to execute command: %v", err), http.StatusInternalServerError)
		return
	}

	// 返回纯文本,保留原始格式
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(output))
}

// API3: 结束会话
func handleEndSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	if err := sessionManager.EndSession(req.SessionID); err != nil {
		http.Error(w, fmt.Sprintf("Failed to end session: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Session ended successfully",
	})
}

func main() {
	sessionManager = NewSessionManager()

	http.HandleFunc("/start-session", handleStartSession)
	http.HandleFunc("/run-command", handleRunCommand)
	http.HandleFunc("/end-session", handleEndSession)

	log.Println("Server starting on port 8833...")
	if err := http.ListenAndServe(":8833", nil); err != nil {
		log.Fatal(err)
	}
}
