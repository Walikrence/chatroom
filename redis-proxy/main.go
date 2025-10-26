package main

import (
	"context"
	"log"
	"net"

	"github.com/go-redis/redis/v8"
	pb   "my-web-socket/user/user" // 替换为实际项目路径
	"google.golang.org/grpc"
)

// Redis 客户端
var rdb *redis.Client
var ctx = context.Background()

// 服务实现
type userServiceServer struct {
	pb.UnimplementedUserServiceServer
}

// 注册用户（检查重名）
func (s *userServiceServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	username := req.User.Username
	password := req.User.Password

	// 检查用户是否已存在
	exists, err := rdb.Exists(ctx, "user:"+username).Result()
	if err != nil {
		return &pb.RegisterResponse{Success: false, Message: "Redis 错误"}, err
	}
	if exists > 0 {
		return &pb.RegisterResponse{Success: false, Message: "用户名已存在"}, nil
	}

	// 存储用户信息（实际项目需加密密码）
	err = rdb.Set(ctx, "user:"+username, password, 0).Err()
	if err != nil {
		return &pb.RegisterResponse{Success: false, Message: "注册失败"}, err
	}

	return &pb.RegisterResponse{Success: true, Message: "注册成功"}, nil
}

// 登录验证
func (s *userServiceServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	username := req.User.Username
	password := req.User.Password

	// 获取存储的密码
	storedPassword, err := rdb.Get(ctx, "user:"+username).Result()
	if err == redis.Nil {
		return &pb.LoginResponse{Success: false, Message: "用户名不存在"}, nil
	}
	if err != nil {
		return &pb.LoginResponse{Success: false, Message: "Redis 错误"}, err
	}

	// 验证密码
	if storedPassword != password {
		return &pb.LoginResponse{Success: false, Message: "密码错误"}, nil
	}

	return &pb.LoginResponse{Success: true, Message: "登录成功"}, nil
}

func main() {
	// 连接 Redis（默认本地 6379，无密码）
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // Redis 密码
		DB:       0,  // 默认数据库
	})

	// 测试 Redis 连接
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("无法连接 Redis: %v", err)
	}
	log.Println("已连接 Redis")

	// 启动 gRPC 服务（监听 50051 端口）
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, &userServiceServer{})
	log.Println("redis-proxy 服务启动在 :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}