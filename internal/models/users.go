package models

import "golang.org/x/crypto/bcrypt"

// Role — роль пользователя
type Role string

const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleAdmin  Role = "admin"
)

// User — таблица users
type User struct {
	ID           uint   `gorm:"primaryKey"`
	Email        string `gorm:"uniqueIndex"`
	Phone        string `gorm:"uniqueIndex"`
	Username     string `gorm:"uniqueIndex;not null"` // ← никнейм (логин)
	PasswordHash string `gorm:"not null"`
	Role         Role   `gorm:"type:varchar(16);not null;default:'buyer'"`
}

// HashPassword превращает обычный пароль в безопасный хэш
func HashPassword(pw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(hash), err
}

// CheckPassword проверяет пароль на совпадение с хэшем
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
