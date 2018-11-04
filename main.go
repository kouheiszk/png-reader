package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
)

func uncompress(data []byte) ([]byte, error) {
	dataBuffer := bytes.NewReader(data)
	r, err := zlib.NewReader(dataBuffer)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var buffer bytes.Buffer
	_, err = buffer.ReadFrom(r)
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func bitsPerPixel(colorType int, depth int) (int, error) {
	switch colorType {
	case 0:
		return depth, nil
	case 2:
		return depth * 3, nil
	case 3:
		return depth, nil
	case 4:
		return depth * 2, nil
	case 6:
		return depth * 4, nil
	default:
		return 0, fmt.Errorf("unknown color type")
	}
}

func applyFilter(data []byte, width, height, bitsPerPixel, bytesPerPixel int) ([]byte, error) {
	rowSize := 1 + (bitsPerPixel*width+7)/8
	imageData := make([]byte, width*height*bytesPerPixel)
	rowData := make([]byte, rowSize)
	prevRowData := make([]byte, rowSize)
	for y := 0; y < height; y++ {
		offset := y * rowSize
		rowData = data[offset : offset+rowSize]
		filterType := int(rowData[0])

		currentScanData := rowData[1:]
		prevScanData := prevRowData[1:]

		switch filterType {
		case 0:
			// No-op.
		case 1:
			for i := bytesPerPixel; i < len(currentScanData); i++ {
				currentScanData[i] += currentScanData[i-bytesPerPixel]
			}
		case 2:
			for i, p := range prevScanData {
				currentScanData[i] += p
			}
		case 3:
			for i := 0; i < bytesPerPixel; i++ {
				currentScanData[i] += prevScanData[i] / 2
			}
			for i := bytesPerPixel; i < len(currentScanData); i++ {
				currentScanData[i] += uint8((int(currentScanData[i-bytesPerPixel]) + int(prevScanData[i])) / 2)
			}
		case 4:
			var a, b, c, pa, pb, pc int
			for i := 0; i < bytesPerPixel; i++ {
				a, c = 0, 0
				for j := i; j < len(currentScanData); j += bytesPerPixel {
					b = int(prevScanData[j])
					pa = b - c
					pb = a - c
					pc = int(math.Abs(float64(pa + pb)))
					pa = int(math.Abs(float64(pa)))
					pb = int(math.Abs(float64(pb)))
					if pa <= pb && pa <= pc {
						// No-op.
					} else if pb <= pc {
						a = b
					} else {
						a = c
					}
					a += int(currentScanData[j])
					a &= 0xff
					currentScanData[j] = uint8(a)
					c = b
				}
			}
		default:
			return nil, fmt.Errorf("bad filter type")
		}

		copy(imageData[y*len(currentScanData):], currentScanData)

		prevRowData, rowData = rowData, prevRowData
	}

	return imageData, nil
}

func parse(r io.Reader) (img image.Image, err error) {
	buffer := new(bytes.Buffer)
	_, err = buffer.ReadFrom(r)
	if err != nil {
		return
	}

	//　PNGシグネチャの読み込み
	if string(buffer.Next(8)) != "\x89PNG\r\n\x1a\n" {
		return nil, fmt.Errorf("not a PNG")
	}

	// IHDRチャンクの読み込み
	_ = buffer.Next(4)
	if string(buffer.Next(4)) != "IHDR" {
		return nil, fmt.Errorf("invalid")
	}
	width := int(binary.BigEndian.Uint32(buffer.Next(4)))
	height := int(binary.BigEndian.Uint32(buffer.Next(4)))
	depth := int(buffer.Next(1)[0])
	colorType := int(buffer.Next(1)[0])
	if int(buffer.Next(1)[0]) != 0 {
		return nil, fmt.Errorf("unknown compression method")
	}
	if int(buffer.Next(1)[0]) != 0 {
		return nil, fmt.Errorf("unknown filter method")
	}
	interlace := int(buffer.Next(1)[0]) == 1
	_ = buffer.Next(4) // CRC
	fmt.Println("width:", width, "height:", height, "depth:", depth, "colorType:", colorType, "interlace:", interlace)

	// IDATチャンクの読み込み
	data := make([]byte, 0, 32)
	loop := true
	for loop {
		length := int(binary.BigEndian.Uint32(buffer.Next(4)))
		chunkType := string(buffer.Next(4))

		switch chunkType {
		case "IDAT":
			fmt.Println("chunk: IDAT")
			data = append(data, buffer.Next(length)...)
			_ = buffer.Next(4) // CRC
		case "IEND":
			fmt.Println("chunk: IEND")
			loop = false
		default:
			fmt.Println("chunk:", chunkType)
			_ = buffer.Next(length) // chunk data
			_ = buffer.Next(4)      // CRC
		}
	}
	fmt.Println("data length:", len(data))

	// 画像データの展開
	data, err = uncompress(data)
	if err != nil {
		return
	}
	fmt.Println("uncompressed data length:", len(data))

	// フィルタタイプの適用
	bitsPerPixel, err := bitsPerPixel(colorType, depth)
	if err != nil {
		return
	}
	bytesPerPixel := (bitsPerPixel + 7) / 8
	data, err = applyFilter(data, width, height, bitsPerPixel, bytesPerPixel)
	if err != nil {
		return
	}
	fmt.Println("applied filter type data length:", len(data))

	// 色情報の抽出
	nrgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := bytesPerPixel*width*y + bytesPerPixel*x
			pixel := data[offset : offset+bytesPerPixel]
			i := y*nrgba.Stride + x*4
			nrgba.Pix[i] = pixel[0]   // R
			nrgba.Pix[i+1] = pixel[1] // G
			nrgba.Pix[i+2] = pixel[2] // B
			nrgba.Pix[i+3] = 255      // A
		}
	}
	img = nrgba

	return
}

func main() {
	inputFilePath := filepath.Join("images", "lenna.png")
	inputFile, err := os.Open(inputFilePath)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer inputFile.Close()

	img, err := parse(inputFile)
	if err != nil {
		fmt.Println(err)
		return
	}

	outputFile, err := os.Create("output.png")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer outputFile.Close()

	png.Encode(outputFile, img)
	fmt.Println("Complete")
}
