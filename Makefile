.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	go build -o CNGBot .

.PHONY: up
up: build
	./CNGBot

.PHONY: get-token
get-token:
	go run get_token.go

.PHONY: get_token
get_token: get-token

.PHONY: restart
restart:
	systemctl restart cngbot

.PHONY: logs
logs: 
	journalctl -u cngbot -f -n 1000

.PHONY: update
update: 
	git update

.PHONY: deploy
deploy: update build up-prod
	
.PHONY: git-config-setup
git-config-setup:
	git config --global alias.update '!env GIT_EDITOR=: git pull --ff'
