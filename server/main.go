package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	pb "my-web-socket/user/user" // 修正包路径（去掉多余的/user）
	"google.golang.org/grpc"
)

// 数据结构定义
type Session struct {
	Username  string
	ExpiresAt time.Time
}

// 全局变量
var (
	grpcClient pb.UserServiceClient
	sessions   = make(map[string]Session) // 会话存储
	clients    = make(map[*websocket.Conn]string) // WebSocket连接
	broadcast  = make(chan Message)
	mu         sync.Mutex
	upgrader   = websocket.Upgrader{ // WebSocket升级器
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

// 消息结构
type Message struct {
	Type     string `json:"type"` // "userJoined", "userLeft", "message"
	Username string `json:"username"`
	Content  string `json:"content,omitempty"`
}

// 生成随机会话ID
func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// 检查登录状态
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

// 初始化 gRPC 客户端
func initGRPCClient() {
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure()) // 生产环境需用安全连接
	if err != nil {
		log.Fatalf("无法连接 gRPC 服务: %v", err)
	}
	grpcClient = pb.NewUserServiceClient(conn)
	log.Println("已连接 redis-proxy 服务")
}

// 注册 API（改为调用 gRPC）
func registerHandler(w http.ResponseWriter, r *http.Request) {
	var req pb.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"无效的请求"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" || len(req.Password) < 6 {
		http.Error(w, `{"message":"用户名或密码无效"}`, http.StatusBadRequest)
		return
	}

	// 调用 gRPC 注册接口
	resp, err := grpcClient.Register(context.Background(), &pb.RegisterRequest{
		User: &req,
	})
	if err != nil {
		http.Error(w, `{"message":"注册服务异常"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if !resp.Success {
		w.WriteHeader(http.StatusConflict)
	}
	json.NewEncoder(w).Encode(resp)
}

// 登录 API（改为调用 gRPC）
func loginHandler(w http.ResponseWriter, r *http.Request) {
	var req pb.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"无效的请求"}`, http.StatusBadRequest)
		return
	}

	// 调用 gRPC 登录接口
	resp, err := grpcClient.Login(context.Background(), &pb.LoginRequest{
		User: &req,
	})
	if err != nil {
		http.Error(w, `{"message":"登录服务异常"}`, http.StatusInternalServerError)
		return
	}

	if resp.Success {
		// 生成会话
		sessionID := generateSessionID()
		mu.Lock()
		sessions[sessionID] = Session{
			Username:  req.Username,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		mu.Unlock()

		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    sessionID,
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: true,
			Path:     "/",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if !resp.Success {
		w.WriteHeader(http.StatusUnauthorized)
	}
	json.NewEncoder(w).Encode(resp)
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
	// 初始化 gRPC 客户端
	initGRPCClient()

	// 启动服务器
	http.Handle("/", http.FileServer(http.Dir("./public")))
	http.HandleFunc("/api/register", registerHandler)
	http.HandleFunc("/api/login", loginHandler)
	http.HandleFunc("/api/check-login", checkLoginHandler)
	http.HandleFunc("/api/logout", logoutHandler)
	http.HandleFunc("/ws", websocketHandler)

	go broadcastMessages()

	log.Println("聊天室服务器启动在 :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}