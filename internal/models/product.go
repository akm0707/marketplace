package models

// Product — таблица products
type Product struct {
	Base
	SellerID    uint   `gorm:"index;not null"`
	Title       string `gorm:"not null"`
	Description string `gorm:"type:text"`
	PriceCents  int    `gorm:"not null"`
	Stock       int    `gorm:"not null;default:0"`
	ImagePath   string // относительный путь, напр. "/uploads/abc123.jpg"
}
