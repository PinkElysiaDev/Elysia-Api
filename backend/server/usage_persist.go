package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/elysia-api/backend/config"
)

func (s *Server) usagePersistPath() string {
	return filepath.Join(s.config.Dir(), "usage-records.jsonl")
}

func (s *Server) loadUsageRecords() error {
	if !s.config.IsUsagePersistEnabled() {
		return nil
	}

	path := s.usagePersistPath()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	maxRecords := s.config.GetUsagePersistMaxRecords()
	records := make([]usageRecord, 0)
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record usageRecord
		if err := json.Unmarshal(line, &record); err != nil {
			log.Printf("failed to parse usage record from %s: %v", path, err)
			continue
		}
		records = append(records, record)
		if len(records) > maxRecords {
			copy(records, records[len(records)-maxRecords:])
			records = records[:maxRecords]
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	s.usageMu.Lock()
	s.usageRecords = records
	s.usageMu.Unlock()
	return nil
}

func (s *Server) appendUsageRecordLocked(record usageRecord) {
	if !s.config.IsUsagePersistEnabled() {
		return
	}

	path := s.usagePersistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("failed to create usage persistence directory: %v", err)
		return
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("failed to open usage persistence file: %v", err)
		return
	}
	defer file.Close()

	payload, err := json.Marshal(record)
	if err != nil {
		log.Printf("failed to marshal usage record: %v", err)
		return
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		log.Printf("failed to append usage record: %v", err)
	}
}

func (s *Server) compactUsageRecordsLocked() {
	maxRecords := s.config.GetUsagePersistMaxRecords()
	if len(s.usageRecords) <= maxRecords {
		return
	}
	copy(s.usageRecords, s.usageRecords[len(s.usageRecords)-maxRecords:])
	s.usageRecords = s.usageRecords[:maxRecords]
	if s.config.IsUsagePersistEnabled() {
		s.rewriteUsageRecordsLocked()
	}
}

func (s *Server) rewriteUsageRecordsLocked() {
	path := s.usagePersistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("failed to create usage persistence directory: %v", err)
		return
	}

	// 先在内存拼好完整内容再原子替换：O_TRUNC 直写在压缩重写中途崩溃会
	// 丢掉全部历史记录，而不是只丢最后一条。
	var buf bytes.Buffer
	for _, record := range s.usageRecords {
		payload, err := json.Marshal(record)
		if err != nil {
			log.Printf("failed to marshal usage record during compact: %v", err)
			continue
		}
		buf.Write(payload)
		buf.WriteByte('\n')
	}
	if err := config.WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		log.Printf("failed to rewrite usage persistence file: %v", err)
	}
}

func (s *Server) clearUsageRecords() error {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	s.usageRecords = nil
	path := s.usagePersistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(path, nil, 0o600); err != nil {
		return fmt.Errorf("failed to clear usage persistence file: %w", err)
	}
	return nil
}
