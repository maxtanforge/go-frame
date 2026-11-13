package greetinghttp

// Input declares the HTTP request shape; business rules remain in Service.
type Input struct {
	Name string `json:"name" v:"present" label:"姓名"`
}
