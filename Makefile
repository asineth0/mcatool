all:
	@rm -rf build
	@mkdir -p build
	@GOOS=linux go build -ldflags '-s -w' -o build/mcatool-linux
	@GOOS=darwin go build -ldflags '-s -w' -o build/mcatool-macos
	@GOOS=windows go build -ldflags '-s -w' -o build/mcatool-windows.exe
	@ls -lha build/*