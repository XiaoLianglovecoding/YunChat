package service

import "fmt"

// NotImplementedError 让未来的 Handler 能统一映射为 HTTP 501。
type NotImplementedError struct {
	TaskID  string
	Feature string
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("TODO[%s]: %s", e.TaskID, e.Feature)
}

func NewTODO(taskID, feature string) error {
	return &NotImplementedError{TaskID: taskID, Feature: feature}
}
