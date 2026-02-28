package main

import (
	"log"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/server"
	"github.com/elysia-api/backend/signal"
)

func main() {
	if config.GlobalConfig == nil {
		log.Fatal("Config not loaded")
	}

	// 启动心跳监控（超时时间从 config.GlobalConfig.HeartbeatTimeout 获取）
	// 如果配置中未设置，使用默认值 300 秒
	signal.StartHeartbeatMonitor(config.GlobalConfig.GetHeartbeatTimeout())

	srv := server.New(config.GlobalConfig)

	// 注册心跳端点
	srv.RegisterHeartbeatHandler(signal.HandleHeartbeat)

	log.Printf("Starting Elysia-API backend on %s:%d",
		config.GlobalConfig.Server.Host,
		config.GlobalConfig.Server.Port)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
