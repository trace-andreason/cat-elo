package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
	"io/ioutil"
)

var (
	cats []Cat
	mu   sync.Mutex // Mutex to protect access to cats slice

	// Load the secrets from environment variables
	managementKey = os.Getenv("MANAGEMENT_KEY")
	deliveryKey   = os.Getenv("DELIVERY_KEY")
	apiKey         = os.Getenv("API_KEY")
)

type Response struct {
	Entries []struct {
		UID   string `json:"uid"`
		File  struct {
			URL string `json:"url"`
		} `json:"file"`
		Title string `json:"title"`
	} `json:"entries"`
}

type Cat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Image string `json:"image"`
	ELO   int    `json:"elo"`
}

type ByELO []Cat

func (a ByELO) Len() int           { return len(a) }
func (a ByELO) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByELO) Less(i, j int) bool { return a[i].ELO > a[j].ELO }

func loadCats() ([]Cat, error) {
	url := "https://cdn.contentstack.io/v3/content_types/cat/entries"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %v", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("authorization", managementKey)
	req.Header.Add("access_token", deliveryKey)
	req.Header.Add("api_key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %v", err)
	}

	var response Response
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling body: %v", err)
	}
	cats := []Cat{}
	for _, entry := range response.Entries {
		cats = append(cats, Cat{
			ID:    entry.UID,
			Title: entry.Title,
			Image: entry.File.URL,
			ELO:   1200,
		})
	}
	return cats, nil
}

// Function to generate a random Cat ID
func getRandomCat(excludeID string) Cat {
	rand.Seed(time.Now().UnixNano())
	// Select a random cat excluding the one with the given ID
	var availableCats []Cat
	for _, cat := range cats {
		if cat.ID != excludeID {
			availableCats = append(availableCats, cat)
		}
	}

	// Return a random cat from the remaining options
	return availableCats[rand.Intn(len(availableCats))]
}

func getTopCats(w http.ResponseWriter, r *http.Request) {
	mu.Lock() // Lock the mutex before accessing the cats array
	defer mu.Unlock() // Ensure to unlock after function execution

	// Sort the cats slice by ELO descending
	sort.Sort(ByELO(cats))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cats[:5])
}

func getRandomCats(w http.ResponseWriter, r *http.Request) {
	mu.Lock() // Lock the mutex before accessing the cats array
	defer mu.Unlock() // Ensure to unlock after function execution

	// Select two distinct random cats
	cat1 := getRandomCat("")
	cat2 := getRandomCat(cat1.ID) // Ensure cat2 is different from cat1

	// Respond with the two random cats in JSON format
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode([2]Cat{cat1, cat2})
}

func calculateELO(winnerELO, loserELO int) (int, int) {
	k := 32 // K-factor, determines how much the ratings can change after a match

	// Calculate expected scores
	expectedWinner := 1 / (1 + math.Pow(10, float64(loserELO-winnerELO)/400))
	expectedLoser := 1 / (1 + math.Pow(10, float64(winnerELO-loserELO)/400))

	// Update ELO ratings
	winnerNewELO := winnerELO + int(float64(k)*(1-expectedWinner))
	loserNewELO := loserELO + int(float64(k)*(0-expectedLoser))

	return winnerNewELO, loserNewELO
}

func matchResult(w http.ResponseWriter, r *http.Request) {
	mu.Lock() // Lock the mutex before accessing the cats array
	defer mu.Unlock() // Ensure to unlock after function execution

	var matchData struct {
		WinnerID string `json:"winner_id"`
		LoserID  string `json:"loser_id"`
	}

	// Decode the incoming JSON request
	err := json.NewDecoder(r.Body).Decode(&matchData)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Find the winner and loser cats by ID
	var winner, loser *Cat
	for i := range cats {
		if cats[i].ID == matchData.WinnerID {
			winner = &cats[i]
		} else if cats[i].ID == matchData.LoserID {
			loser = &cats[i]
		}
	}

	// If winner or loser is not found, return an error
	if winner == nil || loser == nil {
		http.Error(w, "Invalid cat IDs", http.StatusBadRequest)
		return
	}

	// Update the ELO for both cats based on the result of the match
	winELO, loseELO := calculateELO(winner.ELO, loser.ELO)
	winner.ELO = winELO
	loser.ELO = loseELO

	// Generate a new random cat to face the winning cat
	newOpponent := getRandomCat(winner.ID)

	// Respond with the updated winner, loser, and the new opponent
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(struct {
		Winner       Cat `json:"winner"`
		NewOpponent  Cat `json:"new_opponent"`
	}{
		Winner:      *winner,
		NewOpponent: newOpponent,
	})
}

func reloadCats(w http.ResponseWriter, r *http.Request) {
	mu.Lock() // Lock the mutex before modifying the cats array
	defer mu.Unlock() // Ensure to unlock after the function execution

	// Reload cats from the external source
	localCats, err := loadCats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reloading cats: %v", err), http.StatusInternalServerError)
		return
	}

	// Map existing cats by ID to easily preserve ELO values
	existingCats := make(map[string]*Cat)
	for i := range cats {
		existingCats[cats[i].ID] = &cats[i]
	}

	// Clear the existing cats slice to prevent duplication
	cats = []Cat{}

	// Update the global cats slice, preserving ELO for existing cats and setting ELO for new cats
	for _, localCat := range localCats {
		if existingCat, exists := existingCats[localCat.ID]; exists {
			// Preserve the existing ELO
			localCat.ELO = existingCat.ELO
		} else {
			// Assign a default ELO of 1200 for new cats
			localCat.ELO = 1200
		}
		// Add the cat (new or existing) to the cats slice
		cats = append(cats, localCat)
	}

	// Respond with the success message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Cats reloaded successfully"})
}

func main() {
	// Make sure the required environment variables are set
	if managementKey == "" || deliveryKey == "" || apiKey == "" {
		fmt.Println("Error: Missing required environment variables.")
		os.Exit(1)
	}

	localCats, err := loadCats()
	if err != nil {
		fmt.Printf("error loading cats: %v", err)
		return
	}

	mu.Lock() // Lock the mutex before modifying the global cats slice
	cats = localCats
	mu.Unlock() // Unlock after modifying the cats slice

	// Serve static files and handle API calls
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	http.HandleFunc("/api/random", getRandomCats)
	http.HandleFunc("/api/match", matchResult)
	http.HandleFunc("/api/top", getTopCats)
	http.HandleFunc("/api/reload", reloadCats) // Add the new route for reloading cats

	// Start the server on port 8080
	fmt.Println("Server is starting at http://localhost:80")
	err = http.ListenAndServe(":80", nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}
}
