// Package yolov3 provides a go implementation of the yolov3 object detection system: https://pjreddie.com/darknet/yolo/
package yolov3

import (
	"fmt"
	"image"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"gocv.io/x/gocv"
)

const (
	inputWidth  = 416
	inputHeight = 416

	confThreshold = 0.5
	nmsThreshold  = 0.4
)

// Config optional config of the net
type Config struct {
	InputWidth          int
	InputHeight         int
	ConfidenceThreshold float32
	NMSThreshold        float32

	NetTargetType  gocv.NetTargetType
	NetBackendType gocv.NetBackendType
}

// DefaultConfig creates new default config
func DefaultConfig() Config {
	return Config{
		InputWidth:          inputWidth,
		InputHeight:         inputHeight,
		ConfidenceThreshold: confThreshold,
		NMSThreshold:        nmsThreshold,
		NetTargetType:       gocv.NetTargetCPU,
		NetBackendType:      gocv.NetBackendDefault,
	}
}

// ObjectDetection represents information of a detected object
type ObjectDetection struct {
	ClassID     int
	ClassName   string
	BoundingBox image.Rectangle
	Confidence  float32
}

// Net the yolov3 net
type Net interface {
	Close() error
	GetDetections(gocv.Mat) ([]ObjectDetection, error)
}

// the net implementation
type yoloNet struct {
	net       gocv.Net
	cocoNames []string

	inputWidth          int
	inputHeight         int
	confidenceThreshold float32
	nmsThreshold        float32
}

// NewNet creates new yolo net for given weight path, config and coconames list
func NewNet(weightPath, configPath, cocoNamePath string) (Net, error) {
	return NewNetWithConfig(weightPath, configPath, cocoNamePath, DefaultConfig())
}

// NewNetWithConfig creates new yolo net with given config
func NewNetWithConfig(weightPath, configPath, cocoNamePath string, config Config) (Net, error) {
	if _, err := os.Stat(weightPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("path to net weight not found")
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("path to net config not found")
	}

	cocoNames, err := getCocoNames(cocoNamePath)
	if err != nil {
		return nil, err
	}

	net := gocv.ReadNet(weightPath, configPath)

	err = net.SetPreferableBackend(config.NetBackendType)
	if err != nil {
		return nil, err
	}

	err = net.SetPreferableTarget(config.NetTargetType)
	if err != nil {
		return nil, err
	}

	return &yoloNet{
		net:                 net,
		cocoNames:           cocoNames,
		inputWidth:          config.InputWidth,
		inputHeight:         config.InputHeight,
		confidenceThreshold: config.ConfidenceThreshold,
		nmsThreshold:        config.NMSThreshold,
	}, nil
}

// Close closes the net
func (y *yoloNet) Close() error {
	return y.net.Close()
}

// GetDetections retrieve predicted detections from given matrix
func (y *yoloNet) GetDetections(frame gocv.Mat) ([]ObjectDetection, error) {
	fl := getOutputsNames(&y.net)
	// fl := []string{"yolo_82", "yolo_94", "yolo_106"}
	blob := gocv.BlobFromImage(frame, 1.0/255.0, image.Pt(y.inputWidth, y.inputHeight), gocv.NewScalar(0, 0, 0, 0), true, false)
	defer func() {
		err := blob.Close()
		if err != nil {
			log.WithError(err).Error("unable to close blob")
		}
	}()
	y.net.SetInput(blob, "data")

	outputs := y.net.ForwardLayers(fl)
	detections, err := y.processOutputs(frame, outputs)
	if err != nil {
		return nil, err
	}

	return detections, nil
}

func getOutputsNames(net *gocv.Net) []string {
	var outputLayers []string
	for _, i := range net.GetUnconnectedOutLayers() {
		layer := net.GetLayer(i)
		layerName := layer.GetName()
		if layerName != "_input" {
			outputLayers = append(outputLayers, layerName)
		}
	}
	return outputLayers
}

// processOutputs process detected rows in the outputs
func (y *yoloNet) processOutputs(frame gocv.Mat, outputs []gocv.Mat) ([]ObjectDetection, error) {
	detections := []ObjectDetection{}
	bboxes := []image.Rectangle{}
	confidences := []float32{}
	for i := 0; i < len(outputs); i++ {
		output := outputs[i]
		data, err := output.DataPtrFloat32()
		if err != nil {
			return nil, err
		}
		for i := 0; i < output.Total(); i += output.Cols() {
			row := data[i : i+output.Cols()]
			scores := row[5:]
			classID, confidence := getClassIDAndConfidence(scores)
			if confidence > y.confidenceThreshold {
				confidences = append(confidences, confidence)

				boundingBox := calculateBoundingBox(frame, row)
				bboxes = append(bboxes, boundingBox)
				detections = append(detections, ObjectDetection{
					ClassID:     classID,
					ClassName:   y.cocoNames[classID],
					BoundingBox: boundingBox,
					Confidence:  confidence,
				})
			}
		}
	}
	if len(bboxes) == 0 {
		return detections, nil
	}
	indices := make([]int, len(bboxes))

	gocv.NMSBoxes(bboxes, confidences, y.confidenceThreshold, y.nmsThreshold, indices)
	result := []ObjectDetection{}
	for i, indice := range indices {
		// If we encounter value 0 skip the detection
		// except for the first indice
		if i != 0 && indice == 0 {
			continue
		}
		result = append(result, detections[indice])
	}
	return result, nil
}

// calculateBoundingBox calculate the bounding box of the detected object
func calculateBoundingBox(frame gocv.Mat, row []float32) image.Rectangle {
	centerX := int(row[0] * float32(frame.Cols()))
	centerY := int(row[1] * float32(frame.Rows()))
	width := int(row[2] * float32(frame.Cols()))
	height := int(row[3] * float32(frame.Rows()))
	left := (centerX - width/2)
	top := (centerY - height/2)
	return image.Rect(left, top, left+width, top+height)
}

// getClassID retrieve class id from given row
func getClassIDAndConfidence(x []float32) (int, float32) {
	res := 0
	max := float32(0.0)
	for i, y := range x {
		if y > max {
			max = y
			res = i
		}
	}
	return res, max
}

// getCocoNames read coconames from given path
func getCocoNames(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(content), "\n"), nil
}
