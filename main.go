package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Global variables
var jwtSecretKey = []byte(getEnv("JWT_SECRET", "your_secret_key"))
var db *gorm.DB

// User defines the schema for the "users" table.
type User struct {
	ID               uint   `gorm:"primaryKey"`
	Username         string `gorm:"unique;not null"`
	Email            string `gorm:"unique;not null"`
	Password         string `gorm:"not null"` // stored as a bcrypt hash
	AccountType      string `gorm:"not null"` // e.g., "free" or "paid"
	IsPublic         bool   `gorm:"default:true"`
	State            string // user's state or location
	StripeCustomerID string // for paid accounts
	BooksRead        int    `gorm:"default:0"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Request structures for binding and validation
type SignupRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	State    string `json:"state" binding:"required"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func main() {
	// Initialize the database connection and run migrations
	setupDatabase()

	// Set Gin mode based on environment variable; default to release
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = gin.ReleaseMode
	}
	gin.SetMode(ginMode)

	router := gin.Default()

	// Endpoints for signup and login
	router.POST("/signup", signupHandler)
	router.POST("/login", loginHandler)

	// Protected routes group
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.GET("/profile", profileHandler)
	}

	// Use port from env or default to 8082
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	log.Printf("Auth service is listening on port %s", port)

	for _, r := range router.Routes() {
		log.Printf("→ %s %s", r.Method, r.Path)
	}

	router.Run(":" + port)
}

func setupDatabase() {
	// Get database configuration from environment variables or use defaults
	dbHost := getEnv("DB_HOST", "")
	dbUser := getEnv("DB_USER", "")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "")
	dbPort := getEnv("DB_PORT", "")

	// DSN for PostgreSQL connection
	dsn := "host=" + dbHost +
		" user=" + dbUser +
		" password=" + dbPassword +
		" dbname=" + dbName +
		" port=" + dbPort +
		" sslmode=disable TimeZone=UTC"

	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Could not connect to the database: %v", err)
	}

	// AutoMigrate creates or updates the "users" table based on the User model
	if err := db.AutoMigrate(&User{}); err != nil {
		log.Fatalf("AutoMigrate failed: %v", err)
	}
	log.Println("Database connected and migrated")
}

// signupHandler validates input and creates a new user in the database
func signupHandler(c *gin.Context) {
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid signup data", "details": err.Error()})
		return
	}

	// Hash the password using bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Create a new user with default free account type and public profile
	user := User{
		Username:    req.Username,
		Email:       req.Email,
		Password:    string(hashedPassword),
		AccountType: "free",
		IsPublic:    true,
		State:       req.State,
	}

	// Check if a user with the same username or email already exists
	var existing User
	if err := db.Where("username = ? OR email = ?", user.Username, user.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User with this username or email already exists"})
		return
	}

	// Save the user to the database
	if err := db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register user", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User registered", "user_id": user.ID})
}

// loginHandler validates credentials and returns a JWT token
func loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid login data", "details": err.Error()})
		return
	}

	// Find the user by username
	var user User
	if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// Compare the provided password with the stored hashed password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// Create JWT token with user claims
	claims := jwt.MapClaims{
		"username": user.Username,
		"user_id":  user.ID,
		"exp":      time.Now().Add(time.Hour * 72).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecretKey)
	if err != nil {
		log.Printf("Error signing token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

// profileHandler returns user profile info by querying the database using claims from the token
func profileHandler(c *gin.Context) {
	// Retrieve the claims set in the middleware
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Claims not found"})
		return
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	// Extract user_id from token claims (note: JSON numbers are float64)
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	// Query the user from the database
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	// Return user profile details (excluding sensitive fields like password)
	c.JSON(http.StatusOK, gin.H{
		"username":     user.Username,
		"email":        user.Email,
		"account_type": user.AccountType,
		"is_public":    user.IsPublic,
		"state":        user.State,
		"books_read":   user.BooksRead,
		"created_at":   user.CreatedAt,
	})
}

// authMiddleware validates the JWT token from the Authorization header.
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := extractToken(c.GetHeader("Authorization"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Ensure that the token method conforms to what you expect:
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecretKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		// Save claims in context for later handlers to use
		c.Set("claims", token.Claims)
		c.Next()
	}
}

// extractToken extracts the token string from the header.
// It expects the header to be in the format "Bearer <token>".
func extractToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("Authorization header missing")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("Authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}

// getEnv returns the value of the environment variable if set, otherwise returns fallback.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
