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
	pb  "my-web-socket/user" // 替换为实际项目路径
	"google.golang.org/grpc"
)

// 移除原有的 users 映射，改为 gRPC 客户端
var (
	grpcClient pb.UserServiceClient
	// 其他全局变量保持不变（sessions, clients, broadcast 等）
)

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
		// 生成会话（逻辑不变）
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

// 其他函数（checkLoginHandler, websocketHandler 等）保持不变

func main() {
	// 初始化 gRPC 客户端
	initGRPCClient()

	// 启动服务器（逻辑不变）
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