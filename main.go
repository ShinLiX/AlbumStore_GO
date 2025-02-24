package main

import (
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// Album represents the album metadata and image information
type Album struct {
	ID        int64  `json:"albumID"`
	ImageURL  string `json:"image_url"`
	Artist    string `json:"artist"`
	Title     string `json:"title"`
	Year      string `json:"year"`
	ImageSize int64  `json:"imageSize"`
}

var db *sql.DB
var s3Session *s3.S3
var bucket string

func main() {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Fatal("DB_DSN environment variable not set")
	}

	bucket = os.Getenv("S3_BUCKET")
	if bucket == "" {
		log.Fatal("S3_BUCKET environment variable not set")
	}

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("us-west-2"),
	})
	if err != nil {
		log.Fatalf("Failed to initialize S3 session: %v", err)
	}
	s3Session = s3.New(sess)

	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatalf("Failed to connect to DB: %v", err)
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS albums (
		id INT AUTO_INCREMENT PRIMARY KEY,
		image_url VARCHAR(255),
		artist VARCHAR(255),
		title VARCHAR(255),
		year VARCHAR(10)
	) ENGINE=InnoDB;
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.POST("/albums", newAlbum)
	r.GET("/albums/:albumID", getAlbumByKey)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r.Run(":" + port)
}

// newAlbum handles the POST /albums endpoint
func newAlbum(c *gin.Context) {
	// Parse the multipart form
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image file"})
		return
	}
	defer file.Close()

	artist := c.PostForm("profile[artist]")
	title := c.PostForm("profile[title]")
	year := c.PostForm("profile[year]")

	// Upload image to S3
	imageURL, imageSize, err := uploadToS3(file, header)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload image to S3"})
		return
	}

	// Store album metadata in the database
	res, err := db.Exec(
		"INSERT INTO albums (image_url, artist, title, year) VALUES (?, ?, ?, ?)",
		imageURL, artist, title, year,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	id, _ := res.LastInsertId()
	c.JSON(http.StatusOK, gin.H{
		"albumID":   id,
		"imageSize": imageSize,
	})
}

// getAlbumByKey handles the GET /albums/{albumID} endpoint
func getAlbumByKey(c *gin.Context) {
	idStr := c.Param("albumID")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid album ID"})
		return
	}

	var album Album
	row := db.QueryRow("SELECT id, image_url, artist, title, year FROM albums WHERE id = ?", id)
	if err := row.Scan(&album.ID, &album.ImageURL, &album.Artist, &album.Title, &album.Year); err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"msg": "Key not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, album)
}

// uploadToS3 uploads an image to S3 and returns the image URL and size
func uploadToS3(file multipart.File, header *multipart.FileHeader) (string, int64, error) {
	key := fmt.Sprintf("images/%s%s", uuid.New().String(), filepath.Ext(header.Filename))

	tempFile, err := ioutil.TempFile("", "upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tempFile.Name())

	fileSize, err := io.Copy(tempFile, file)
	if err != nil {
		return "", 0, err
	}

	tempFile.Seek(0, 0)

	_, err = s3Session.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        tempFile,
		ContentType: aws.String(header.Header.Get("Content-Type")),
		ACL:         aws.String("public-read"),
	})
	if err != nil {
		return "", 0, err
	}

	imageURL := fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, key)
	return imageURL, fileSize, nil
}
