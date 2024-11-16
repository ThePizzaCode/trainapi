package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// AdjustTime handles overnight train times
func AdjustTime(t string) string {
	// Split time into components
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return t // Return as is if not in HH:MM:SS format
	}

	// Convert hours, minutes, and seconds
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return t // Return as is if there's an error
	}

	// Adjust for overnight times
	if hours >= 24 {
		hours -= 24
	}

	// Reconstruct the time string
	return fmt.Sprintf("%02d:%s:%s", hours, parts[1], parts[2])
}

// Structs for GTFS files
type Stop struct {
	StopID   string `json:"stop_id"`
	StopName string `json:"stop_name"`
	Lat      string `json:"stop_lat"`
	Lon      string `json:"stop_lon"`
}

type StopTime struct {
	TripID        string `json:"trip_id"`
	StopID        string `json:"stop_id"`
	ArrivalTime   string `json:"arrival_time"`
	DepartureTime string `json:"departure_time"`
	StopSequence  int    `json:"stop_sequence"`
}

type Trip struct {
	TripID    string `json:"trip_id"`
	RouteID   string `json:"route_id"`
	ServiceID string `json:"service_id"`
}

type Calendar struct {
	ServiceID string  `json:"service_id"`
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
	Weekdays  [7]bool // Mon to Sun: true if service runs that day
}

// TrainInfoResponse defines the structure of the JSON response
type TrainInfoResponse struct {
	TrainNumber string      `json:"train_number"`
	Date        string      `json:"date"`
	Stops       []TrainStop `json:"stops"`
}

type TrainStop struct {
	StopName      string `json:"stop_name"`
	ArrivalTime   string `json:"arrival_time"`
	DepartureTime string `json:"departure_time"`
}

// TrainListResponse defines the structure for a list of trains
type TrainListResponse struct {
	Departure string        `json:"departure"`
	Arrival   string        `json:"arrival"`
	Date      string        `json:"date"`
	Trains    []TrainDetail `json:"trains"`
}

type TrainDetail struct {
	TrainNumber string      `json:"train_number"`
	Stops       []TrainStop `json:"stops"`
}

// Load GTFS Stops from stops.txt
func loadStops() (map[string]Stop, error) {
	file, err := os.Open("gtfs/stops.txt")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	stops := make(map[string]Stop)
	for _, record := range records[1:] { // Skip header
		if len(record) < 4 { // Ensure there are enough columns in the record
			continue // Skip this record if it doesn't have enough fields
		}

		stop := Stop{
			StopID:   record[0],
			StopName: record[1],
			Lat:      record[2],
			Lon:      record[3],
		}

		stops[stop.StopID] = stop
	}
	return stops, nil
}

// Load GTFS Stop Times from stop_times.txt
func loadStopTimes() ([]StopTime, error) {
	file, err := os.Open("gtfs/stop_times.txt")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var stopTimes []StopTime
	for _, record := range records[1:] { // Skip header
		stopSequence, _ := strconv.Atoi(record[4]) // Convert stop_sequence to int
		stopTime := StopTime{
			TripID:        record[0],
			StopID:        record[3],
			ArrivalTime:   record[1],
			DepartureTime: record[2],
			StopSequence:  stopSequence,
		}

		// Don't skip records with missing times
		stopTimes = append(stopTimes, stopTime)
	}
	return stopTimes, nil
}

// Load GTFS Trips from trips.txt
func loadTrips() ([]Trip, error) {
	file, err := os.Open("gtfs/trips.txt")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var trips []Trip
	for _, record := range records[1:] { // Skip header
		trip := Trip{
			TripID:    record[2],
			RouteID:   record[0],
			ServiceID: record[1],
		}
		trips = append(trips, trip)
	}
	return trips, nil
}

// Load calendar.txt for service dates
func loadCalendar() ([]Calendar, error) {
	file, err := os.Open("gtfs/calendar.txt")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var calendars []Calendar
	for _, record := range records[1:] {
		weekdays := [7]bool{
			record[1] == "1", // Monday
			record[2] == "1", // Tuesday
			record[3] == "1", // Wednesday
			record[4] == "1", // Thursday
			record[5] == "1", // Friday
			record[6] == "1", // Saturday
			record[7] == "1", // Sunday
		}

		cal := Calendar{
			ServiceID: record[0],
			StartDate: record[8],
			EndDate:   record[9],
			Weekdays:  weekdays,
		}
		calendars = append(calendars, cal)
	}
	return calendars, nil
}

// Check if a trip is valid on a given date
func isValidTrip(trip Trip, date time.Time, calendars []Calendar) bool {
	for _, cal := range calendars {
		if cal.ServiceID == trip.ServiceID {
			// Check if the date is within the valid date range
			startDate, _ := time.Parse("20060102", cal.StartDate)
			endDate, _ := time.Parse("20060102", cal.EndDate)

			if date.Before(startDate) || date.After(endDate) {
				log.Printf("Trip %s not valid on %s: outside service date range (%s - %s)\n", trip.TripID, date, startDate, endDate)
				continue
			}

			// Check if the service runs on the day of the week
			weekday := date.Weekday()
			if cal.Weekdays[weekday] {
				return true
			} else {
				log.Printf("Trip %s not valid on %s: service does not run on %s\n", trip.TripID, date, weekday)
			}
		}
	}
	return false
}

// Handle API request and return JSON response for specific train info
func handleGetTrainInfo(w http.ResponseWriter, r *http.Request) {
	log.Println("request received for /getTrainInfo")
	trainNumber := r.URL.Query().Get("trainNumber")
	date := r.URL.Query().Get("date")

	// Parse the date and validate
	parsedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	// Load GTFS data
	stops, err := loadStops()
	if err != nil {
		http.Error(w, "Error loading stops", http.StatusInternalServerError)
		return
	}

	stopTimes, err := loadStopTimes()
	if err != nil {
		http.Error(w, "Error loading stop times", http.StatusInternalServerError)
		return
	}

	trips, err := loadTrips()
	if err != nil {
		http.Error(w, "Error loading trips", http.StatusInternalServerError)
		return
	}

	calendars, err := loadCalendar()
	if err != nil {
		http.Error(w, "Error loading calendar", http.StatusInternalServerError)
		return
	}

	// Filter trips by train number
	var matchingTrips []Trip
	for _, trip := range trips {
		if trip.TripID == trainNumber && isValidTrip(trip, parsedDate, calendars) {
			matchingTrips = append(matchingTrips, trip)
		}
	}

	// If no trips found for the train number
	if len(matchingTrips) == 0 {
		http.Error(w, "No trips found for this train number and date", http.StatusNotFound)
		return
	}

	var stopsWithTimes []TrainStop
	for _, trip := range matchingTrips {
		for _, stopTime := range stopTimes {
			if stopTime.TripID == trip.TripID {
				stop := stops[stopTime.StopID]
				arrivalTime := AdjustTime(stopTime.ArrivalTime)
				departureTime := AdjustTime(stopTime.DepartureTime)

				// Check for empty times and set placeholders if necessary
				if arrivalTime == "" {
					arrivalTime = "N/A" // or any placeholder you prefer
				}
				if departureTime == "" {
					departureTime = "N/A" // or any placeholder you prefer
				}

				stopsWithTimes = append(stopsWithTimes, TrainStop{
					StopName:      stop.StopName,
					ArrivalTime:   arrivalTime,
					DepartureTime: departureTime,
				})
			}
		}
	}

	// Create response
	response := TrainInfoResponse{
		TrainNumber: trainNumber,
		Date:        date,
		Stops:       stopsWithTimes,
	}

	// Send JSON response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Handle API request and return JSON response
// Handle API request and return JSON response
func handleGetTrainList(w http.ResponseWriter, r *http.Request) {
	log.Println("Request received for /getTrainList")
	departureStation := r.URL.Query().Get("departureStation")
	arrivalStation := r.URL.Query().Get("arrivalStation")
	date := r.URL.Query().Get("date")

	// Parse the date and validate
	parsedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	// Load GTFS data
	stops, err := loadStops()
	if err != nil {
		http.Error(w, "Error loading stops", http.StatusInternalServerError)
		return
	}

	stopTimes, err := loadStopTimes()
	if err != nil {
		http.Error(w, "Error loading stop times", http.StatusInternalServerError)
		return
	}

	trips, err := loadTrips()
	if err != nil {
		http.Error(w, "Error loading trips", http.StatusInternalServerError)
		return
	}

	calendars, err := loadCalendar()
	if err != nil {
		http.Error(w, "Error loading calendar", http.StatusInternalServerError)
		return
	}

	// Find stop IDs for departure and arrival stations
	var departureID, arrivalID string
	for _, stop := range stops {
		if stop.StopName == departureStation {
			departureID = stop.StopID
		}
		if stop.StopName == arrivalStation {
			arrivalID = stop.StopID
		}
	}

	// If either station is not found
	if departureID == "" || arrivalID == "" {
		http.Error(w, "Departure or Arrival station not found", http.StatusNotFound)
		return
	}

	// Collect trains containing both stops with their departure and arrival times
	type TrainInfo struct {
		TripID        string `json:"trip_id"`
		DepartureTime string `json:"departure_time"`
		ArrivalTime   string `json:"arrival_time"`
	}

	var trainsWithStops []TrainInfo

	for _, trip := range trips {
		if !isValidTrip(trip, parsedDate, calendars) {
			continue
		}

		var departureTime, arrivalTime string
		var hasDeparture, hasArrival bool

		for _, stopTime := range stopTimes {
			if stopTime.TripID == trip.TripID {
				if stopTime.StopID == departureID {
					hasDeparture = true
					departureTime = AdjustTime(stopTime.DepartureTime) // Adjust time here
				}
				if stopTime.StopID == arrivalID {
					hasArrival = true
					arrivalTime = AdjustTime(stopTime.ArrivalTime) // Adjust time here
				}
			}
			// Check if we found both departure and arrival, and stop if we already have both
			if hasDeparture && hasArrival {
				break
			}
		}

		// Check if we have valid departure and arrival times and if the stops are in the correct order
		if hasDeparture && hasArrival && departureTime != "" && arrivalTime != "" {
			// Ensure the stop order is correct by comparing indices
			departureIndex := findStopIndex(trip.TripID, departureID, stopTimes)
			arrivalIndex := findStopIndex(trip.TripID, arrivalID, stopTimes)

			if departureIndex < arrivalIndex {
				trainsWithStops = append(trainsWithStops, TrainInfo{
					TripID:        trip.TripID,   // Store the trip ID
					DepartureTime: departureTime, // Departure time
					ArrivalTime:   arrivalTime,   // Arrival time
				})
			}
		}
	}

	// If no trains found
	if len(trainsWithStops) == 0 {
		http.Error(w, "No trains found containing both specified stations", http.StatusNotFound)
		return
	}

	// Return the results as JSON
	response := struct {
		DepartureStation string      `json:"departure_station"`
		ArrivalStation   string      `json:"arrival_station"`
		Date             string      `json:"date"`
		Trains           []TrainInfo `json:"trains"` // Use TrainInfo to include times
	}{
		DepartureStation: departureStation,
		ArrivalStation:   arrivalStation,
		Date:             parsedDate.Format("2006-01-02"),
		Trains:           trainsWithStops,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// findStopIndex finds the index of a stop in the stopTimes slice for a specific tripID
func findStopIndex(tripID string, stopID string, stopTimes []StopTime) int {
	for i, stopTime := range stopTimes {
		if stopTime.TripID == tripID && stopTime.StopID == stopID {
			return i
		}
	}
	return -1 // Not found
}

func main() {
	http.HandleFunc("/getTrainInfo", handleGetTrainInfo)
	http.HandleFunc("/getTrainList", handleGetTrainList)

	log.Println("Starting server on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
