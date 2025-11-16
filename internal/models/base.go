package models

import "time"

// Base — общие поля для всех таблиц
type Base struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
