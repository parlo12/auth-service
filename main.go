package main

import (
	"net/http"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
)

// varaiable for the JWT secret key

var jwtSecretKey = []byte("your_secret_key")

func main() {
	router := gin.Default()

	router.POST("/signup", signupHandler)
	router.POST("/login", loginHandler)

	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.GET("/profile", profileHandler)
	}

	router.Run(":8080")
}

func signupHandler(c *gin.Context) {
	// Parse input, validate, and store user in PostgreSQL
	c.JSON(http.StatusOK, gin.H{"message": "User registered"})
}

func loginHandler(c *gin.Context) {
	// Validate credentials, then issue JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": "sampleuser",
		"exp":      time.Now().Add(time.Hour * 72).Unix(),
	})
	tokenString, _ := token.SignedString(jwtSecret)
	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

func profileHandler(c *gin.Context) {
	// Retrieve user info from database
	c.JSON(http.StatusOK, gin.H{"username": "sampleuser", "account_type": "free"})
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		// Validate JWT token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		c.Next()
	}
}
