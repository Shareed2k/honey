package main
import "github.com/melbahja/goph"
func main() {
    var c *goph.Client
    c.Run("")
    c.Upload("", "")
    c.Download("", "")
    c.Close()
}
