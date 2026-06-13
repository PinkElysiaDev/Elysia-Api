package server

import "log"

// 日志级别优先级。数值越大越严重。LogLevel 配置项决定输出阈值：
// 只有 >= 当前阈值的日志才会输出。debug < info < warn < error。
var logLevelPriority = map[string]int{
	"debug": 0,
	"info":  1,
	"warn":  2,
	"error": 3,
}

const defaultLogPriority = 1 // 默认 info

// currentLogThreshold 返回当前 LogLevel 对应的优先级阈值。
// 无效或为空时回退到 info。
func (s *Server) currentLogThreshold() int {
	level := s.config.GetLogLevel()
	if p, ok := logLevelPriority[level]; ok {
		return p
	}
	return defaultLogPriority
}

// logAt 按级别输出日志：仅当该级别 >= 当前 LogLevel 阈值时才写出。
// 这让 LogLevel 真正控制日志量（中危4）。
func (s *Server) logAt(level, format string, args ...interface{}) {
	p, ok := logLevelPriority[level]
	if !ok {
		p = defaultLogPriority
	}
	if p < s.currentLogThreshold() {
		return
	}
	log.Printf("["+level+"] "+format, args...)
}

func (s *Server) logErrorf(format string, args ...interface{}) { s.logAt("error", format, args...) }
func (s *Server) logWarnf(format string, args ...interface{})  { s.logAt("warn", format, args...) }
func (s *Server) logInfof(format string, args ...interface{})  { s.logAt("info", format, args...) }
