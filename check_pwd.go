package main

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJYd0QI4aKe"
	passwords := []string{"password123", "password", "123456", "admin123", "rasmuslerdorf"}
	for _, p := range passwords {
		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(p))
		if err == nil {
			fmt.Printf("匹配！密码是: %s\n", p)
			return
		}
	}
	fmt.Println("未匹配到已知密码，生成新哈希：")
	newHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	fmt.Printf("password123 的新哈希: %s\n", string(newHash))
}
