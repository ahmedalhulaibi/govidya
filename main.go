package main

// What it does:
//
// This example uses the VideoCapture class to capture video from a connected webcam,
// then saves 100 frames to a video file on disk.
//
// How to run:
//
// savevideo [camera ID] [video file]
//
// 		go run ./cmd/savevideo/main.go 0 testvideo.mp4
//

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"net/http"
	_ "net/http/pprof"

	"gocv.io/x/gocv"
)

func main() {
	if len(os.Args) < 7 {
		fmt.Println("How to run:\n\tsavevideo [camera ID] [video file] [recording time seconds] [fps] [img buffer arr size] [enable pprof (true or false)]")
		return
	}

	deviceID, _ := strconv.Atoi(os.Args[1])
	saveFile := os.Args[2]
	recordingTime, _ := strconv.Atoi(os.Args[3])
	fps, _ := strconv.ParseFloat(os.Args[4], 64)
	imgBufferChannelSize, _ := strconv.Atoi(os.Args[5])
	enablePprof, _ := strconv.ParseBool(os.Args[6])

	webcam, writer, img, cols, rows, err := initialize(deviceID, saveFile, fps)
	//window := gocv.NewWindow("Hello")
	//defer window.Close()
	defer webcam.Close()
	img.Close()
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	imgBufferChannel := make(chan *gocv.Mat, imgBufferChannelSize)
	writerSwapChannel := make(chan *gocv.VideoWriter)

	writerSwapTicker := time.NewTicker(time.Duration(recordingTime) * time.Second)

	killImgBufferChannel := make(chan bool)
	killImgWriterChannel := make(chan bool)
	killWriterSwapChannel := make(chan bool)

	doneChannel := make(chan bool)

	go imageBufferRoutine(killImgBufferChannel, killWriterSwapChannel, killImgWriterChannel, doneChannel, imgBufferChannel, webcam)
	//go imageWriterRoutine(killImgBufferChannel, killWriterSwapChannel, killImgWriterChannel, doneChannel, imgBufferChannel, writerSwapChannel, writer, window)
	go imageWriterRoutine(killImgBufferChannel, killWriterSwapChannel, killImgWriterChannel, doneChannel, imgBufferChannel, writerSwapChannel, writer)
	go writerSwapRoutine(killImgBufferChannel, killWriterSwapChannel, killImgWriterChannel, doneChannel, writerSwapChannel, saveFile, fps, cols, rows, writerSwapTicker)
	if enablePprof {
		go func() {
			log.Println(http.ListenAndServe("localhost:6060", nil))
		}()
	}

	fmt.Println("Press enter to stop recording")
	reader := bufio.NewReader(os.Stdin)
	reader.ReadRune()

	killWriterSwapChannel <- true
	killImgBufferChannel <- true
	killImgWriterChannel <- true

	fmt.Println("Kill channel sent")
	<-doneChannel
	<-doneChannel
	<-doneChannel
	fmt.Println("End of pgm")
}

func imageBufferRoutine(killImgBufferChannel chan bool, killWriterSwapChannel chan bool, killImgWriterChannel chan bool, doneChannel chan bool, imgBufferChannel chan *gocv.Mat, webcam *gocv.VideoCapture) {
ImageBufferLoop:
	for {
		select {
		case <-killImgBufferChannel:
			fmt.Println("Kill received ImageBufferLoop")
			break ImageBufferLoop
		default:
			image, err := bufferImage(webcam)
			if err != nil {
				fmt.Println(err.Error())
				killWriterSwapChannel <- true
				killImgWriterChannel <- true
				break ImageBufferLoop
			} else {
				imgBufferChannel <- image
			}
		}
	}
	fmt.Println("End of image buffer routine")
	doneChannel <- true
}

func imageWriterRoutine(killImgBufferChannel chan bool, killWriterSwapChannel chan bool, killImgWriterChannel chan bool, doneChannel chan bool, imgBufferChannel chan *gocv.Mat, writerSwapChannel chan *gocv.VideoWriter, writer *gocv.VideoWriter) {
	var wg sync.WaitGroup
ImageWriterLoop:
	for {
		select {
		case <-killImgWriterChannel:
			fmt.Println("Kill received ImageWriterLoop")
			writer.Close()
			break ImageWriterLoop
		case writerSwapSignal := <-writerSwapChannel:
			fmt.Println("swapping writer")
			var oldWriter *gocv.VideoWriter
			oldWriter = writer
			//fmt.Printf("oldWriter closed isOpened = %v\nClosing old writer", oldWriter.IsOpened())
			err := oldWriter.Close()
			if err != nil {
				fmt.Println(err.Error())
				killImgBufferChannel <- true
				killWriterSwapChannel <- true
				break ImageWriterLoop
			}
			writer = writerSwapSignal
			//fmt.Println("writer swapped")
			//fmt.Printf("new writer isOpened = %v\n", writer.IsOpened())
		case image := <-imgBufferChannel:
			//window.IMShow(*image)
			//window.WaitKey(1)
			wg.Add(1)
			writeImage(writer, image, &wg)
			wg.Wait()
		}
	}
	fmt.Println("End of image writer routine")
	doneChannel <- true
}

func writerSwapRoutine(killImgBufferChannel chan bool, killWriterSwapChannel chan bool, killImgWriterChannel chan bool, doneChannel chan bool, writerSwapChannel chan *gocv.VideoWriter, saveFile string, fps float64, cols int, rows int, writerSwapTicker *time.Ticker) {
WriterSwapLoop:
	for {
		select {
		case <-killWriterSwapChannel:
			writerSwapTicker.Stop()
			fmt.Println("Kill received WriterSwapLoop")
			break WriterSwapLoop
		case <-writerSwapTicker.C:
			// fmt.Println("creating new writer")
			newWriterPtr, err := newWriter(saveFile, fps, cols, rows)
			fmt.Printf("saveFile: %v\tfps: %f\tcols: %d\trows: %d\n", saveFile, fps, cols, rows)
			// //time.Sleep(time.Second)
			if err != nil {
				fmt.Println(err.Error())
				killImgWriterChannel <- true
				killImgBufferChannel <- true
				doneChannel <- true
				break WriterSwapLoop
			} else {
				fmt.Println("signal new writer")
				writerSwapChannel <- newWriterPtr
			}
		}
	}
	fmt.Println("End of writer swap routine")
	doneChannel <- true
}

func initialize(deviceID int, saveFile string, fps float64) (*gocv.VideoCapture, *gocv.VideoWriter, *gocv.Mat, int, int, error) {
	fmt.Println("initialize Called")
	webcam, err := gocv.VideoCaptureDevice(int(deviceID))
	if err != nil {
		fmt.Printf("error opening video capture device: %v\n", deviceID)
		return nil, nil, nil, 0, 0, err
	}
	img := gocv.NewMat()
	defer img.Close()

	if ok := webcam.Read(&img); !ok {
		return nil, nil, nil, 0, 0, fmt.Errorf("cannot read device %d", deviceID)
	}

	writer, err := newWriter(saveFile, fps, img.Cols(), img.Rows())
	if err != nil {
		fmt.Printf("error opening video writer device: %v\n", saveFile)
		return nil, nil, nil, 0, 0, err
	}

	return webcam, writer, &img, img.Cols(), img.Rows(), nil
}

func bufferImage(webcam *gocv.VideoCapture) (*gocv.Mat, error) {
	//fmt.Println("bufferImage Called")

	img := gocv.NewMat()
	if ok := webcam.Read(&img); !ok {
		return nil, fmt.Errorf("cannot read device")
	}
	return &img, nil
}

func writeImage(writer *gocv.VideoWriter, img *gocv.Mat, wg *sync.WaitGroup) {
	//fmt.Println("writeImage Called")
	//fmt.Printf("Writer val: %v \n Image val: %v\n", writer, img)
	if writer.IsOpened() {
		//fmt.Println("Writing image")
		err := writer.Write(*img)
		if err != nil {
			fmt.Println(err.Error())
		} else {
			//fmt.Println("Wrote image to file")
		}
	} else {
		//fmt.Println("Writer is closed!")
	}
	img.Close()
	wg.Done()
}

func newWriter(saveFile string, fps float64, cols int, rows int) (*gocv.VideoWriter, error) {
	//fmt.Println("newWriter Called")
	t := time.Now()
	timestamp := t.Format("2006-01-02T150405")
	filename := strings.Join([]string{timestamp, saveFile}, "-")
	writer, err := gocv.VideoWriterFile(filename, "MJPG", fps, cols, rows, true)
	if err != nil {
		//fmt.Printf("error opening video writer device: %v\n", saveFile)
		return nil, err
	}

	return writer, nil
}
