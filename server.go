package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 数据结构定义
type User struct {
	Username string
	Password string
}

type Message struct {
	Type     string `json:"type"` // "userJoined", "userLeft", "message"
	Username string `json:"username"`
	Content  string `json:"content,omitempty"`
}

type Session struct {
	Username  string
	ExpiresAt time.Time
}

// 全局存储（实际项目中应使用数据库）
var (
	// 用户存储（用户名 -> 用户信息）
	users = make(map[string]User)
	// 会话存储（会话ID -> 会话信息）
	sessions = make(map[string]Session)
	// WebSocket连接存储（连接 -> 用户名）
	clients = make(map[*websocket.Conn]string)
	
	broadcast = make(chan Message)
	mu        sync.Mutex
)

// WebSocket升级器
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// 生成随机会话ID
func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// 检查会话是否有效
func getCurrentUser(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		return "", false
	}

	mu.Lock()
	defer mu.Unlock()
	session, exists := sessions[cookie.Value]
	if !exists || time.Now().After(session.ExpiresAt) {
		return "", false
	}

	// 延长会话有效期
	session.ExpiresAt = time.Now().Add(24 * time.Hour)
	sessions[cookie.Value] = session
	return session.Username, true
}

// 注册API
func registerHandler(w http.ResponseWriter, r *http.Request) {
	var req User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"无效的请求"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" || len(req.Password) < 6 {
		http.Error(w, `{"message":"用户名或密码无效"}`, http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	if _, exists := users[req.Username]; exists {
		http.Error(w, `{"message":"用户名已存在"}`, http.StatusConflict)
		return
	}

	// 存储用户（实际项目中应加密密码）
	users[req.Username] = req
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message":"注册成功"}`))
}

// 登录API
func loginHandler(w http.ResponseWriter, r *http.Request) {
	var req User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"无效的请求"}`, http.StatusBadRequest)
		return
	}

	mu.Lock()
	user, exists := users[req.Username]
	mu.Unlock()

	if !exists || user.Password != req.Password {
		http.Error(w, `{"message":"用户名或密码错误"}`, http.StatusUnauthorized)
		return
	}

	// 创建会话
	sessionID := generateSessionID()
	mu.Lock()
	sessions[sessionID] = Session{
		Username:  req.Username,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	mu.Unlock()

	// 设置会话Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		Path:     "/",
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message":"登录成功"}`))
}

// 检查登录状态API
func checkLoginHandler(w http.ResponseWriter, r *http.Request) {
	username, ok := getCurrentUser(r)
	if !ok {
		http.Error(w, `{"message":"未登录"}`, http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"username": username})
}

// 退出登录API
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err == nil {
		mu.Lock()
		delete(sessions, cookie.Value)
		mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Path:     "/",
	})

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"message":"退出成功"}`))
}

// WebSocket处理
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	// 验证登录状态
	username, ok := getCurrentUser(r)
	if !ok {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}

	// 升级连接
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("升级失败:", err)
		return
	}
	defer conn.Close()

	// 添加到客户端列表并广播上线消息
	mu.Lock()
	clients[conn] = username
	mu.Unlock()
	broadcast <- Message{Type: "userJoined", Username: username}
	log.Printf("用户 %s 上线，当前在线: %d", username, len(clients))

	// 读取消息
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("读取失败:", err)
			break
		}

		var message Message
		if err := json.Unmarshal(msg, &message); err != nil {
			log.Println("解析消息失败:", err)
			continue
		}

		// 补充用户名（防止客户端伪造）
		message.Username = username
		broadcast <- message
	}

	// 下线处理
	mu.Lock()
	delete(clients, conn)
	mu.Unlock()
	broadcast <- Message{Type: "userLeft", Username: username}
	log.Printf("用户 %s 下线，当前在线: %d", username, len(clients))
}

// 广播消息
func broadcastMessages() {
	for msg := range broadcast {
		data, err := json.Marshal(msg)
		if err != nil {
			log.Println("序列化失败:", err)
			continue
		}

		mu.Lock()
		for conn := range clients {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Println("发送失败:", err)
				conn.Close()
				delete(clients, conn)
			}
		}
		mu.Unlock()
	}
}

func main() {
	// 静态文件服务
	http.Handle("/", http.FileServer(http.Dir("./public")))
	
	// API路由
	http.HandleFunc("/api/register", registerHandler)
	http.HandleFunc("/api/login", loginHandler)
	http.HandleFunc("/api/check-login", checkLoginHandler)
	http.HandleFunc("/api/logout", logoutHandler)
	http.HandleFunc("/ws", websocketHandler)

	// 启动广播协程
	go broadcastMessages()

	log.Println("服务器启动在 :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}