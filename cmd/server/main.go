package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	mydb "marketplace/internal/db"
	models "marketplace/internal/models"
)

type ViewData map[string]any

const cartKey = "cart" // map[string]int

func withUser(c *gin.Context, data ViewData) ViewData {
	if data == nil {
		data = ViewData{}
	}
	sess := sessions.Default(c)
	if v := sess.Get("user_email"); v != nil {
		data["UserEmail"] = v.(string)
	}
	if v := sess.Get("user_username"); v != nil {
		data["UserName"] = v.(string)
	}

	// cart count
	count := 0
	if raw := sess.Get(cartKey); raw != nil {
		if m, ok := raw.(map[string]int); ok {
			for _, q := range m {
				count += q
			}
		}
	}
	data["CartCount"] = count
	return data
}

// ---------- auth middlewares ----------
func mustLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)
		if sess.Get("user_email") == nil && sess.Get("user_username") == nil {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

func mustSeller(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)
		if email == "" && username == "" {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		var u models.User
		q := db
		if email != "" {
			q = q.Where("email = ?", email)
		} else {
			q = q.Where("username = ?", username)
		}
		if err := q.First(&u).Error; err != nil {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		role := strings.ToLower(string(u.Role))
		if role != "seller" && role != "admin" {
			c.Redirect(http.StatusSeeOther, "/seller/upgrade")
			c.Abort()
			return
		}
		c.Set("currentUser", &u)
		c.Next()
	}
}

// ---------- uploads helper ----------
func saveUploadedImage(c *gin.Context, field string) (string, error) {
	file, err := c.FormFile(field)
	if err != nil {
		// файл не выбран — не ошибка
		return "", nil
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return "", fmt.Errorf("unsupported image format")
	}
	_ = os.MkdirAll("uploads", 0o755) // <<< гарантируем наличие папки
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	dst := filepath.Join("uploads", name)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		return "", err
	}
	return "/uploads/" + name, nil
}

// ---------- cart in sessions ----------
func getCart(c *gin.Context) map[string]int {
	sess := sessions.Default(c)
	raw := sess.Get(cartKey)
	if raw == nil {
		return map[string]int{}
	}
	m, ok := raw.(map[string]int)
	if !ok {
		return map[string]int{}
	}
	return m
}
func saveCart(c *gin.Context, cart map[string]int) {
	sess := sessions.Default(c)
	sess.Set(cartKey, cart)
	_ = sess.Save()
}
 // helper для проверки наличия файла
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func main() {
	// <<< грузим .env и из текущей папки, и из родительской (когда запускаем из cmd/server)
	// грузим .env из нескольких мест: текущая папка, родительская, корень репо
	_ = godotenv.Overload(".env", "../.env", "../../.env")
 
	if dsn := os.Getenv("DB_DSN"); dsn == "" {
	log.Println("WARN: DB_DSN still empty; CWD check…")
	if wd, err := os.Getwd(); err == nil {
		log.Println("CWD:", wd)
	}
	log.Println("Exists ./.env? ", fileExists(".env"))
	log.Println("Exists ../.env?", fileExists("../.env"))
}


	db := mydb.MustOpen()
	if err := db.AutoMigrate(&models.User{}, &models.Product{}); err != nil {
		log.Fatal(err)
	}

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	r := gin.Default()

	// раздача статики
	r.Static("/uploads", "./uploads")
	r.Static("/static", "./static")

	// sessions
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		secret = "dev_fallback_secret" // <<< дефолт, чтобы не падало на пустом
	}
	store := cookie.NewStore([]byte(secret))
	store.Options(sessions.Options{HttpOnly: true, SameSite: http.SameSiteLaxMode})
	r.Use(sessions.Sessions("mp_session", store))

	// templates
	r.SetFuncMap(template.FuncMap{
		"price": func(cents int) string { return fmt.Sprintf("%.2f", float64(cents)/100.0) },
		"add":   func(a, b int) int { return a + b },
		"sub":   func(a, b int) int { return a - b },
	})
	// <<< рекурсивный паттерн, чтобы находить и internal/views/seller/*.tmpl тоже
	r.LoadHTMLGlob("internal/views/**/*.tmpl")


	// health
	r.GET("/health", func(c *gin.Context) {
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "db": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// JSON
	r.GET("/products", func(c *gin.Context) {
		var items []models.Product
		if err := db.Order("id desc").Find(&items).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, items)
	})

	// Public index
	r.GET("/", func(c *gin.Context) {
		var items []models.Product
		_ = db.Order("id desc").Find(&items).Error
		c.HTML(http.StatusOK, "list.tmpl", withUser(c, ViewData{"Items": items}))
	})

	// Register (email OR phone) + username/password
	r.GET("/register", func(c *gin.Context) {
		c.HTML(http.StatusOK, "register.tmpl", withUser(c, nil))
	})
	r.POST("/register", func(c *gin.Context) {
		contact := strings.TrimSpace(c.PostForm("contact")) // email or phone
		username := strings.TrimSpace(c.PostForm("username"))
		pw := c.PostForm("password")
		if contact == "" || username == "" || pw == "" {
			c.HTML(http.StatusBadRequest, "register.tmpl", withUser(c, ViewData{"Error": "Fill all fields"}))
			return
		}
		var email, phone string
		if strings.Contains(contact, "@") {
			email = contact
		} else {
			phone = contact
		}
		var cnt int64
		db.Model(&models.User{}).Where("username = ?", username).Count(&cnt)
		if cnt > 0 {
			c.HTML(http.StatusBadRequest, "register.tmpl", withUser(c, ViewData{"Error": "Username taken"}))
			return
		}
		if email != "" {
			db.Model(&models.User{}).Where("email = ?", email).Count(&cnt)
			if cnt > 0 {
				c.HTML(http.StatusBadRequest, "register.tmpl", withUser(c, ViewData{"Error": "Email already registered"}))
				return
			}
		}
		if phone != "" {
			db.Model(&models.User{}).Where("phone = ?", phone).Count(&cnt)
			if cnt > 0 {
				c.HTML(http.StatusBadRequest, "register.tmpl", withUser(c, ViewData{"Error": "Phone already registered"}))
				return
			}
		}
		hash, err := models.HashPassword(pw)
		if err != nil {
			c.HTML(http.StatusInternalServerError, "register.tmpl", withUser(c, ViewData{"Error": err.Error()}))
			return
		}
		u := models.User{Username: username, Email: email, Phone: phone, PasswordHash: hash, Role: models.RoleBuyer}
		if err := db.Create(&u).Error; err != nil {
			c.HTML(http.StatusInternalServerError, "register.tmpl", withUser(c, ViewData{"Error": err.Error()}))
			return
		}
		sess := sessions.Default(c)
		sess.Set("user_email", u.Email)
		sess.Set("user_username", u.Username)
		_ = sess.Save()
		c.Redirect(http.StatusSeeOther, "/")
	})

	// Login (username/email/phone + password)
	r.GET("/login", func(c *gin.Context) {
		c.HTML(http.StatusOK, "login.tmpl", withUser(c, nil))
	})
	r.POST("/login", func(c *gin.Context) {
		ident := strings.TrimSpace(c.PostForm("username"))
		pw := c.PostForm("password")
		if ident == "" || pw == "" {
			c.HTML(http.StatusBadRequest, "login.tmpl", withUser(c, ViewData{"Error": "Fill all fields"}))
			return
		}

		var u models.User
		q := db
		if strings.Contains(ident, "@") {
			q = q.Where("email = ?", ident)
		} else if strings.HasPrefix(ident, "+") || strings.IndexFunc(ident, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
			q = q.Where("phone = ?", ident)
		} else {
			q = q.Where("username = ?", ident)
		}

		if err := q.First(&u).Error; err != nil {
			c.HTML(http.StatusUnauthorized, "login.tmpl", withUser(c, ViewData{"Error": "User not found"}))
			return
		}
		if !models.CheckPassword(u.PasswordHash, pw) {
			c.HTML(http.StatusUnauthorized, "login.tmpl", withUser(c, ViewData{"Error": "Wrong password"}))
			return
		}

		sess := sessions.Default(c)
		sess.Set("user_email", u.Email)
		sess.Set("user_username", u.Username)
		_ = sess.Save()
		c.Redirect(http.StatusSeeOther, "/")
	})

	// Logout
	r.GET("/logout", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Clear()
		_ = sess.Save()
		c.Redirect(http.StatusSeeOther, "/")
	})

	// Become seller in one click
	r.GET("/seller/upgrade", mustLogin(), func(c *gin.Context) {
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)

		var u models.User
		q := db
		if email != "" {
			q = q.Where("email = ?", email)
		} else {
			q = q.Where("username = ?", username)
		}
		if err := q.First(&u).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		if strings.ToLower(string(u.Role)) == "seller" {
			c.Redirect(http.StatusSeeOther, "/seller/products")
			return
		}
		u.Role = models.RoleSeller
		if err := db.Save(&u).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Redirect(http.StatusSeeOther, "/seller/products")
	})

	// ------ Seller area ------
	// My products
	r.GET("/seller/products", mustSeller(db), func(c *gin.Context) {
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)

		var u models.User
		q := db
		if email != "" {
			q = q.Where("email = ?", email)
		} else {
			q = q.Where("username = ?", username)
		}
		if err := q.First(&u).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}

		var items []models.Product
		if err := db.Where("seller_id = ?", u.ID).Order("id desc").Find(&items).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.HTML(http.StatusOK, "seller_products.tmpl", withUser(c, ViewData{"Items": items}))
	})

	// New form
	r.GET("/seller/products/new", mustSeller(db), func(c *gin.Context) {
		c.HTML(http.StatusOK, "seller_form.tmpl", withUser(c, ViewData{"Mode": "create"}))
	})

	// Create
	r.POST("/seller/products", mustSeller(db), func(c *gin.Context) {
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)

		var u models.User
		q := db
		if email != "" {
			q = q.Where("email = ?", email)
		} else {
			q = q.Where("username = ?", username)
		}
		if err := q.First(&u).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}

		title := strings.TrimSpace(c.PostForm("title"))
		desc := strings.TrimSpace(c.PostForm("description"))
		price := strings.TrimSpace(c.PostForm("price"))
		stock := strings.TrimSpace(c.PostForm("stock"))
		if title == "" || price == "" || stock == "" {
			c.HTML(http.StatusBadRequest, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "create", "Error": "Fill title, price, stock",
				"Form": ViewData{"Title": title, "Description": desc, "Price": price, "Stock": stock},
			}))
			return
		}
		price = strings.ReplaceAll(price, ",", ".")
		var dollars, cents, priceCents, stockInt int
		if strings.Contains(price, ".") {
			fmt.Sscanf(price, "%d.%d", &dollars, &cents)
			if cents > 99 {
				cents = 99
			}
			priceCents = dollars*100 + cents
		} else {
			fmt.Sscanf(price, "%d", &dollars)
			priceCents = dollars * 100
		}
		fmt.Sscanf(stock, "%d", &stockInt)
		if stockInt < 0 {
			stockInt = 0
		}

		imgPath, imgErr := saveUploadedImage(c, "image")
		if imgErr != nil {
			c.HTML(http.StatusBadRequest, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "create", "Error": imgErr.Error(),
				"Form": ViewData{"Title": title, "Description": desc, "Price": price, "Stock": stock},
			}))
			return
		}

		item := models.Product{
			SellerID:    u.ID,
			Title:       title,
			Description: desc,
			PriceCents:  priceCents,
			Stock:       stockInt,
			ImagePath:   imgPath,
		}
		if err := db.Create(&item).Error; err != nil {
			c.HTML(http.StatusInternalServerError, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "create", "Error": err.Error(),
				"Form": ViewData{"Title": title, "Description": desc, "Price": price, "Stock": stock},
			}))
			return
		}
		c.Redirect(http.StatusSeeOther, "/seller/products")
	})

	// Edit form
	r.GET("/seller/products/:id/edit", mustSeller(db), func(c *gin.Context) {
		id := c.Param("id")
		var item models.Product
		if err := db.First(&item, "id = ?", id).Error; err != nil {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		// ownership
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)
		var u models.User
		if email != "" {
			_ = db.Where("email = ?", email).First(&u).Error
		} else {
			_ = db.Where("username = ?", username).First(&u).Error
		}
		if item.SellerID != u.ID {
			c.String(http.StatusForbidden, "Forbidden")
			return
		}
		c.HTML(http.StatusOK, "seller_form.tmpl", withUser(c, ViewData{
			"Mode": "edit", "Item": item,
			"Form": ViewData{
				"Title": item.Title, "Description": item.Description,
				"Price": fmt.Sprintf("%.2f", float64(item.PriceCents)/100.0),
				"Stock": item.Stock,
			},
		}))
	})

	// Update
	r.POST("/seller/products/:id", mustSeller(db), func(c *gin.Context) {
		id := c.Param("id")
		var item models.Product
		if err := db.First(&item, "id = ?", id).Error; err != nil {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		// ownership
		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)
		var u models.User
		if email != "" {
			_ = db.Where("email = ?", email).First(&u).Error
		} else {
			_ = db.Where("username = ?", username).First(&u).Error
		}
		if item.SellerID != u.ID {
			c.String(http.StatusForbidden, "Forbidden")
			return
		}

		title := strings.TrimSpace(c.PostForm("title"))
		desc := strings.TrimSpace(c.PostForm("description"))
		price := strings.TrimSpace(c.PostForm("price"))
		stock := strings.TrimSpace(c.PostForm("stock"))
		if title == "" || price == "" || stock == "" {
			c.HTML(http.StatusBadRequest, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "edit", "Error": "Fill title, price, stock",
				"Item": item,
				"Form": ViewData{"Title": title, "Description": desc, "Price": price, "Stock": stock},
			}))
			return
		}
		price = strings.ReplaceAll(price, ",", ".")
		var dollars, cents, priceCents, stockInt int
		if strings.Contains(price, ".") {
			fmt.Sscanf(price, "%d.%d", &dollars, &cents)
			if cents > 99 {
				cents = 99
			}
			priceCents = dollars*100 + cents
		} else {
			fmt.Sscanf(price, "%d", &dollars)
			priceCents = dollars * 100
		}
		fmt.Sscanf(stock, "%d", &stockInt)
		if stockInt < 0 {
			stockInt = 0
		}

		// optional new image
		if imgPath, imgErr := saveUploadedImage(c, "image"); imgErr != nil {
			c.HTML(http.StatusBadRequest, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "edit", "Error": imgErr.Error(), "Item": item,
				"Form": ViewData{"Title": title, "Description": desc, "Price": price, "Stock": stock},
			}))
			return
		} else if imgPath != "" {
			item.ImagePath = imgPath
		}

		item.Title = title
		item.Description = desc
		item.PriceCents = priceCents
		item.Stock = stockInt

		if err := db.Save(&item).Error; err != nil {
			c.HTML(http.StatusInternalServerError, "seller_form.tmpl", withUser(c, ViewData{
				"Mode": "edit", "Error": err.Error(), "Item": item,
			}))
			return
		}
		c.Redirect(http.StatusSeeOther, "/seller/products")
	})

	// Delete (безопасно — только один элемент продавца)
	r.POST("/seller/products/:id/delete", mustSeller(db), func(c *gin.Context) {
		id := c.Param("id")

		sess := sessions.Default(c)
		email, _ := sess.Get("user_email").(string)
		username, _ := sess.Get("user_username").(string)

		var u models.User
		if email != "" {
			_ = db.Where("email = ?", email).First(&u).Error
		} else {
			_ = db.Where("username = ?", username).First(&u).Error
		}

		res := db.Where("id = ? AND seller_id = ?", id, u.ID).Delete(&models.Product{})
		if res.Error != nil {
			c.String(http.StatusInternalServerError, res.Error.Error())
			return
		}
		if res.RowsAffected == 0 {
			c.String(http.StatusForbidden, "Not your product or not found")
			return
		}
		c.Redirect(http.StatusSeeOther, "/seller/products")
	})

	// ------ Cart ------
	// add
	r.POST("/cart/add", func(c *gin.Context) {
		id := strings.TrimSpace(c.PostForm("product_id"))
		qtyStr := strings.TrimSpace(c.PostForm("qty"))
		if id == "" {
			c.String(http.StatusBadRequest, "no product")
			return
		}
		if qtyStr == "" {
			qtyStr = "1"
		}
		var qty int
		fmt.Sscanf(qtyStr, "%d", &qty)
		if qty <= 0 {
			qty = 1
		}

		// validate product exists
		var p models.Product
		if err := db.First(&p, "id = ?", id).Error; err != nil {
			c.String(http.StatusNotFound, "product not found")
			return
		}
		if p.Stock <= 0 {
			c.String(http.StatusBadRequest, "out of stock")
			return
		}

		cart := getCart(c)
		cart[id] += qty
		if cart[id] < 1 {
			cart[id] = 1
		}
		saveCart(c, cart)

		c.Redirect(http.StatusSeeOther, "/cart")
	})

	// <<< ВНЕ add-хендлера: update
	r.POST("/cart/update", func(c *gin.Context) {
		id := strings.TrimSpace(c.PostForm("product_id"))
		qtyStr := strings.TrimSpace(c.PostForm("qty"))
		if id == "" {
			c.Redirect(http.StatusSeeOther, "/cart")
			return
		}
		var qty int
		fmt.Sscanf(qtyStr, "%d", &qty)
		cart := getCart(c)
		if qty <= 0 {
			delete(cart, id)
		} else {
			cart[id] = qty
		}
		saveCart(c, cart)
		c.Redirect(http.StatusSeeOther, "/cart")
	})

	// <<< ВНЕ add-хендлера: remove
	r.POST("/cart/remove", func(c *gin.Context) {
		id := strings.TrimSpace(c.PostForm("product_id"))
		if id == "" {
			c.Redirect(http.StatusSeeOther, "/cart")
			return
		}
		cart := getCart(c)
		delete(cart, id)
		saveCart(c, cart)
		c.Redirect(http.StatusSeeOther, "/cart")
	})

	r.GET("/cart", func(c *gin.Context) {
		cart := getCart(c)
		type Row struct {
			Product       models.Product
			Qty           int
			SubtotalCents int
		}
		var rows []Row
		total := 0
		for id, q := range cart {
			var p models.Product
			if err := db.First(&p, "id = ?", id).Error; err == nil {
				sub := p.PriceCents * q
				rows = append(rows, Row{Product: p, Qty: q, SubtotalCents: sub})
				total += sub
			}
		}
		c.HTML(http.StatusOK, "cart.tmpl", withUser(c, ViewData{"Rows": rows, "TotalCents": total}))
	})

	// start
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Server listening on :" + port)
	log.Fatal(r.Run(":" + port))
}
