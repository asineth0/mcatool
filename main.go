package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
)

var start = time.Now()
var sizePre = 0
var sizePost = 0
var regionRegex, _ = regexp.Compile(`region/r.-?\d+.-?\d+.mca$`)

type UnpackJob struct {
	name string
	data []byte
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	if os.Args[1] == "purge" {
		if len(os.Args) < 3 {
			log.Fatal("Missing input path")
		}

		input, err := os.Stat(os.Args[2])

		if err != nil || !input.IsDir() {
			log.Fatal("Invalid input path")
		}

		if len(os.Args) < 4 {
			log.Fatal("Missing purge threshold")
		}

		threshold, err := strconv.ParseUint(os.Args[3], 10, 64)

		if err != nil {
			log.Fatal("Invalid purge threshold")
		}

		var wg sync.WaitGroup
		work := make(chan string)

		for i := 0; i < runtime.GOMAXPROCS(0); i++ {
			go func() {
				for {
					path := <-work
					region, _ := os.ReadFile(path)

					if len(region) == 0 {
						continue
					}

					compChunks := getChunks(region)
					chunks := decompChunks(compChunks)

					valid := 0
					keep := 0

					for i, decomp := range chunks {
						if decomp == nil {
							continue
						}

						tagName := "InhabitedTime"

						for j := 0; j < len(decomp); j++ {
							skip := j + len(tagName)

							if bytes.Equal([]byte(tagName), decomp[j:skip]) {
								time := binary.BigEndian.Uint64(decomp[skip:(skip + 8)])

								if int(time) < (int(threshold)*20)+1 {
									chunks[i] = nil
								} else {
									keep++
								}

								break
							}
						}

						valid++
					}

					if keep == 0 {
						log.Printf("removing %s\n", path)
						os.Remove(path)
						wg.Done()
						continue
					}

					if keep != valid {
						offset := 2
						var locs []byte
						var payloads []byte

						for i, decomp := range chunks {
							if decomp == nil {
								locs = append(locs, make([]byte, 4)...)
								continue
							}

							chunk := compChunks[i]
							size := len(chunk) / 4096
							loc := (offset << 8) + size
							locEncoded := make([]byte, 4)
							binary.BigEndian.PutUint32(locEncoded, uint32(loc))
							locs = append(locs, locEncoded...)
							payloads = append(payloads, chunk...)
							offset += size
						}

						var newRegion []byte
						newRegion = append(newRegion, locs...)
						newRegion = append(newRegion, region[4096:8192]...)
						newRegion = append(newRegion, payloads...)
						sizePost += len(newRegion)

						log.Printf("writing %s (%s)", path, compare(len(region), len(newRegion)))
						os.WriteFile(path, newRegion, 0o644)
					}

					if keep != 0 && keep == valid {
						sizePost += len(region)
					}

					wg.Done()
				}
			}()
		}

		filepath.Walk(os.Args[2], func(path string, info fs.FileInfo, _ error) error {
			size := int(info.Size())

			if size == 0 {
				return nil
			}

			if regionRegex.MatchString(path) {
				sizePre += size
				wg.Add(1)
				work <- path
			}

			return nil
		})

		wg.Wait()
		log.Printf("done in %s (%s)", time.Since(start), compare(sizePre, sizePost))
		return
	}

	if os.Args[1] == "pack" {
		if len(os.Args) < 3 {
			log.Fatal("Missing input path")
		}

		input, err := os.Stat(os.Args[2])

		if err != nil || !input.IsDir() {
			log.Fatal("Invalid input path")
		}

		if len(os.Args) < 4 {
			log.Fatal("Missing output path")
		}

		out, _ := os.Create(os.Args[3])

		var wg sync.WaitGroup
		var mu sync.Mutex
		work := make(chan string)

		for i := 0; i < runtime.GOMAXPROCS(0); i++ {
			go func() {
				for {
					file := <-work
					data, _ := os.ReadFile(file)
					dataPre := len(data)

					if regionRegex.MatchString(file) {
						data = packRegion(data)
					}

					var buf bytes.Buffer
					br := brotli.NewWriterLevel(&buf, 9)
					br.Write(data)
					br.Close()
					data, _ = io.ReadAll(&buf)

					file = file[len(os.Args[2])+1:]
					log.Printf("packing %s (%s)", file, compare(dataPre, len(data)))
					nameSize := make([]byte, 8)
					dataSize := make([]byte, 8)
					n := binary.PutUvarint(nameSize, uint64(len(file)))
					nameSize = nameSize[:n]
					n = binary.PutUvarint(dataSize, uint64(len(data)))
					dataSize = dataSize[:n]
					mu.Lock()
					out.Write(nameSize)
					out.Write([]byte(file))
					out.Write(dataSize)
					out.Write(data)
					mu.Unlock()
					wg.Done()
				}
			}()
		}

		filepath.Walk(os.Args[2], func(path string, info fs.FileInfo, _ error) error {
			if path == os.Args[2] || info.IsDir() {
				return nil
			}

			size := int(info.Size())
			sizePre += size

			if size == 0 {
				return nil
			}

			wg.Add(1)
			work <- path
			return nil
		})

		wg.Wait()
		out.Write([]byte{0})
		out.Close()
		info, _ := os.Stat(os.Args[3])
		log.Printf("done in %s (%s)", time.Since(start), compare(sizePre, int(info.Size())))
		return
	}

	if os.Args[1] == "unpack" {
		if len(os.Args) < 3 {
			log.Fatal("Missing input path")
		}

		if len(os.Args) < 4 {
			log.Fatal("Missing output path")
		}

		src := os.Args[2]
		dest := os.Args[3]

		if info, err := os.Stat(os.Args[2]); err != nil || info.IsDir() {
			log.Fatal("Invalid input path")
		}

		if info, err := os.Stat(os.Args[3]); err == nil && !info.IsDir() {
			log.Fatal("Invalid output path")
		}

		os.Mkdir(dest, 0o755)
		f, err := os.Open(src)

		if err != nil {
			log.Fatal("Failed to open input archive")
		}

		br := bufio.NewReader(f)

		var wg sync.WaitGroup
		work := make(chan *UnpackJob)
		for i := 0; i < runtime.GOMAXPROCS(0); i++ {
			go func() {
				for {
					file := <-work
					log.Printf("unpacking %s\n", file.name)
					loc := path.Join(dest, file.name)
					os.MkdirAll(path.Dir(loc), 0o755)
					buf := bytes.NewReader(file.data)
					br := brotli.NewReader(buf)
					data, _ := io.ReadAll(br)

					if regionRegex.MatchString(file.name) {
						data = unpackRegion(data)
					}

					w, _ := os.Create(loc)
					w.Write(data)
					w.Close()
					wg.Done()
				}
			}()
		}

		for {
			nameSize, _ := binary.ReadUvarint(br)

			if nameSize == 0 {
				break
			}

			name := make([]byte, nameSize)
			io.ReadFull(br, name)
			dataSize, _ := binary.ReadUvarint(br)
			data := make([]byte, dataSize)
			io.ReadFull(br, data)

			wg.Add(1)
			work <- &UnpackJob{
				name: string(name),
				data: data,
			}
		}

		wg.Wait()
		f.Close()
		log.Printf("done in %s", time.Since(start))
		return
	}

	printUsage()
}

func printUsage() {
	log.Println("mcatool 0.0.1")
	log.Println("Usage: mcatool purge <path> <sec>")
	log.Println("Usage: mcatool pack <path> <out>")
	log.Println("Usage: mcatool unpack <path> <out>")
}

func prettySize(b int) string {
	const unit = 1024

	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	div, exp := int64(unit), 0

	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

func compare(a int, b int) string {
	ratio := float64(a) / float64(b)
	percent := int((float64(b) / float64(a)) * 100)

	if math.IsNaN(ratio) {
		ratio = 1
		percent = 100
	}

	return fmt.Sprintf("%s->%s/1:%f/%d%%", prettySize(a), prettySize(b), ratio, percent)
}

func getChunks(region []byte) [][]byte {
	var chunks [][]byte

	for i := 0; i < 1024; i++ {
		loc := binary.BigEndian.Uint32(region[(i * 4):((i * 4) + 4)])

		size := int((loc & 0xff) * 4096)
		offset := int((loc >> 8) * 4096)

		if size == 0 || (size+offset) > len(region) {
			chunks = append(chunks, nil)
			continue
		}

		chunks = append(chunks, region[offset:(offset+size)])
	}

	return chunks
}

func decompChunks(compChunks [][]byte) [][]byte {
	chunks := make([][]byte, 1024)

	for i, comp := range compChunks {
		if comp == nil {
			chunks[i] = nil
			continue
		}

		buf := bytes.NewReader(comp[5 : 5+binary.BigEndian.Uint32(comp[:4])])
		zr, _ := zlib.NewReader(buf)
		chunks[i], _ = io.ReadAll(zr)
	}

	return chunks
}

func packRegion(region []byte) []byte {
	if len(region) == 0 {
		return region
	}

	var packed []byte

	compChunks := getChunks(region)
	chunks := decompChunks(compChunks)

	for _, chunk := range chunks {
		size := make([]byte, 8)
		n := 0

		if chunk != nil {
			n = binary.PutUvarint(size, uint64(len(chunk)))
		} else {
			n = binary.PutUvarint(size, 0)
		}

		size = size[:n]

		packed = append(packed, size...)

		if chunk != nil {
			packed = append(packed, chunk...)
		}
	}

	return packed
}

func unpackRegion(data []byte) []byte {
	var locs []byte
	var payloads []byte
	locOffset := 2

	for len(data) > 0 {
		size, n := binary.Uvarint(data)
		comp := data[n:(n + int(size))]
		data = data[(n + int(size)):]

		if len(comp) == 0 {
			locs = append(locs, make([]byte, 4)...)
			continue
		}

		var buf bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
		zw.Write(comp)
		zw.Close()
		comp, _ = io.ReadAll(&buf)
		header := make([]byte, 5)
		binary.BigEndian.PutUint32(header, uint32(len(comp)))
		header[4] = 2 //zlib type.
		var chunk []byte
		chunk = append(chunk, header...)
		chunk = append(chunk, comp...)
		locSize := (len(chunk) / 4096) + 1
		loc := (locOffset << 8) + locSize
		locEncoded := make([]byte, 4)
		binary.BigEndian.PutUint32(locEncoded, uint32(loc))
		locs = append(locs, locEncoded...)
		payloads = append(payloads, chunk...)
		payloads = append(payloads, make([]byte, (locSize*4096)-len(chunk))...)
		locOffset += locSize
	}

	var unpacked []byte
	unpacked = append(unpacked, locs...)
	unpacked = append(unpacked, make([]byte, 4096)...)
	unpacked = append(unpacked, payloads...)
	return unpacked
}
