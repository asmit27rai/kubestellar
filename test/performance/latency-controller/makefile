ifndef IMAGE
$(error IMAGE is not set. Usage: make <target> IMAGE=<your-image:tag>)
endif

# Build the Go binary (linux/amd64)
build:
	GOOS=linux GOARCH=amd64 go build -o manager main.go

# Build the Docker image (multi-stage). Requires IMAGE arg
docker-build: build
	docker build -t $(IMAGE) .

# Push the image to the registry (expects credentials already configured)
docker-push:
	docker push $(IMAGE)

# Clean artifacts
clean:
	rm -f manager
	docker rmi $(IMAGE) || true
