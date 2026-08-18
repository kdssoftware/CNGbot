.PHONY: test build up get-token get_token

test:
	go test ./...

build:
	go build -o CNGBot .

up: build
	./CNGBot

get-token:
	go run get_token.go

get_token: get-token
