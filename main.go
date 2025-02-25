package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

var db *sql.DB

// AlbumMetadata represents the metadata of an album
type AlbumMetadata struct {
	Artist string `json:"artist"`
	Title  string `json:"title"`
	Year   string `json:"year"`
}

// AlbumInfo represents the information returned by the GET endpoint
type AlbumInfo struct {
	AlbumID  int           `json:"albumID"`
	ImageURL string        `json:"image_url"`
	Metadata AlbumMetadata `json:"metadata"`
}

func main() {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Fatal("DB_DSN environment variable not set")
	}

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}

	err = db.Ping()
	if err != nil {
		log.Fatalf("Failed to connect to DB: %v", err)
	}

	// Create the albums table if not exists
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS albums (
		id INT AUTO_INCREMENT PRIMARY KEY,
		image_url VARCHAR(255),
		metadata JSON
	) ENGINE=InnoDB;
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	// Setup Gin engine
	r := gin.Default()

	// Health check route
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// POST /albums -> uploads image and stores metadata
	r.POST("/albums", func(c *gin.Context) {
		// Parse the image file and metadata
		imageFile, err := c.FormFile("image")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image file"})
			return
		}

		artist := c.PostForm("artist")
		title := c.PostForm("title")
		year := c.PostForm("year")

		// Save the image locally
		imagePath, err := saveImageLocally(imageFile)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Prepare metadata as JSON
		metadata := AlbumMetadata{
			Artist: artist,
			Title:  title,
			Year:   year,
		}

		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encode metadata"})
			return
		}

		// Store image URL and metadata in the database
		res, err := db.Exec("INSERT INTO albums (image_url, metadata) VALUES (?, ?)", imagePath, metadataJSON)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		id, _ := res.LastInsertId()
		c.JSON(200, gin.H{"albumID": id, "imagePath": imagePath})
	})

	// GET /albums/{albumID} -> retrieves album info
	r.GET("/albums/:albumID", func(c *gin.Context) {
		albumID := c.Param("albumID")
		var album AlbumInfo
		var metadataJSON string

		row := db.QueryRow("SELECT id, image_url, metadata FROM albums WHERE id = ?", albumID)
		if err := row.Scan(&album.AlbumID, &album.ImageURL, &metadataJSON); err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": "Album not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if err := json.Unmarshal([]byte(metadataJSON), &album.Metadata); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decode metadata"})
			return
		}

		c.JSON(200, album)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s ...", port)
	r.Run(":" + port)
}

// saveImageLocally saves the uploaded image to the local file system
func saveImageLocally(imageFile *multipart.FileHeader) (string, error) {
	imageDir := "./images"
	if err := os.MkdirAll(imageDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create image directory: %v", err)
	}

	filePath := filepath.Join(imageDir, imageFile.Filename)
	file, err := imageFile.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open uploaded image: %v", err)
	}
	defer file.Close()

	out, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to save image: %v", err)
	}
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		return "", fmt.Errorf("failed to write image file: %v", err)
	}

	return filePath, nil
}
