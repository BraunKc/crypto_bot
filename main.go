package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

type CoinInfo struct {
	BuyPrice     float64
	FullQuantity float64
	Quantity     float64
	SellPercent  float64
	SellLevel    int64
	BuyTime      time.Time
}

type Data struct {
	Wallet     float64
	Profit     float64
	TotalPrice float64
	Coins      map[string]CoinInfo
}

func parser(dataJson *Data) {
	// WebSocket URL
	u := url.URL{Scheme: "wss", Host: "pumpportal.fun", Path: "/api/data"}
	log.Printf("Connecting to %s", u.String())

	// Dial the WebSocket server
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("Dial failed:", err)
	}
	defer c.Close()

	// Subscribe to token creation events
	subscribePayload := map[string]string{
		"method": "subscribeNewToken",
	}
	subscribeData, err := json.Marshal(subscribePayload)
	if err != nil {
		log.Fatal("Failed to marshal subscribe payload:", err)
	}
	err = c.WriteMessage(websocket.TextMessage, subscribeData)
	if err != nil {
		log.Fatal("Failed to send subscribe message:", err)
	}

	// Set up a channel to receive interrupt signals
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// Start a goroutine to read messages from the WebSocket
	go func() {
		defer c.Close()
		for {
			if dataJson.Wallet < 1 {
				continue
			}
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}
			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Println("Failed to unmarshal message:", err)
				continue
			}

			mint := msg["mint"]
			if mint != nil {
				price, err := getPrice(mint.(string))
				if err == io.EOF {
					dataJson.Wallet -= 1

					currentTime := time.Now().Format("2006-01-02 15:04:05")
					symbol, ok := msg["symbol"].(string)
					if !ok {
						log.Println("Ошибка: значение'symbol' не является строкой")
						continue
					}
					mint, ok := msg["mint"].(string)
					if !ok {
						log.Println("Ошибка: значение'mint' не является строкой")
						continue
					}
					data := fmt.Sprintf("BUY: %s, %s, %s, %f, %d\n", currentTime, mint, symbol, 0.000005, 200000)
					f, err := os.OpenFile("coins.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
					if err != nil {
						log.Println("Ошибка при открытии файла:", err)
						continue
					}
					defer f.Close()
					_, err = f.WriteString(data)
					if err != nil {
						log.Println("Ошибка при записи в файл:", err)
					}

					dataJson.Coins[mint] = CoinInfo{
						BuyPrice:     0.000005,
						FullQuantity: 200000,
						Quantity:     200000,
						SellPercent:  0.4,
						SellLevel:    0,
						BuyTime:      time.Now(),
					}
					save_data(*dataJson)
					continue
				}
				fmt.Println(price)
			}
		}
	}()
	// Block until an interrupt signal is received
	<-done
	log.Println("Interrupt signal received, closing WebSocket connection.")
}

type RaydiumResponse struct {
	ID      string            `json:"id"`
	Success bool              `json:"success"`
	Data    map[string]string `json:"data"`
}

func getPrice(mint string) (float64, error) {
	url := fmt.Sprintf("https://api-v3.raydium.io/mint/price?mints=%s", mint)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var response RaydiumResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return 0, err
	}

	if !response.Success {
		return 0, fmt.Errorf("Ошибка запроса: %s", response.ID)
	}

	price, ok := response.Data[mint]
	if !ok {
		return 0, fmt.Errorf("Не найдена цена для мемкойна %s", mint)
	}

	priceFloat, err := parseFloat(price)
	if err != nil {
		return 0, err
	}

	return priceFloat, nil
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func save_data(data Data) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = ioutil.WriteFile("data.json", dataBytes, 0644)
	if err != nil {
		fmt.Println(err)
		return
	}
}

func updateCoins(dataJson *Data) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			totalPrice := 0.0
			wallet := dataJson.Wallet
			fmt.Println("check tokens started")
			for mint, coin := range dataJson.Coins {
				price := checkConditions(mint, coin, dataJson)
				totalPrice += price * coin.Quantity
			}
			dataJson.TotalPrice = wallet + totalPrice
			save_data(*dataJson)
		}
	}
}

func checkConditions(mint string, coin CoinInfo, dataJson *Data) float64 {
	price, err := getPrice(mint)
	if err != nil {
		return 0.000005
	}

	if time.Since(coin.BuyTime) > 24*time.Hour {
		sellCoins(mint, coin, price, dataJson, true)
	}

	targets := []float64{2, 11, 101, 1001, 10001, 100001, 1000001}
	if price >= coin.BuyPrice*targets[coin.SellLevel] {
		sellCoins(mint, coin, price, dataJson, false)
	}
	return price
}

func sellCoins(mint string, coin CoinInfo, price float64, dataJson *Data, all bool) {
	if all || coin.SellLevel > 6 {
		fmt.Println("selling all or last coins", mint)
		dataJson.Wallet += (coin.Quantity * price)
		dataJson.Profit += (coin.Quantity * price)
		delete(dataJson.Coins, mint)
		save_data(*dataJson)
	} else {
		fmt.Println("selling part of the coins", mint)
		dataJson.Wallet += (coin.Quantity * coin.SellPercent * price)
		dataJson.Profit += (coin.Quantity * coin.SellPercent * price)
		coin.Quantity -= (coin.FullQuantity * coin.SellPercent)
		coin.SellPercent = 0.1
		coin.SellLevel += 1
		dataJson.Coins[mint] = coin
		save_data(*dataJson)
	}
}

func main() {
	var data Data
	if _, err := os.Stat("data.json"); err == nil {
		dataBytes, err := ioutil.ReadFile("data.json")
		if err != nil {
			fmt.Println(err)
			return
		}
		err = json.Unmarshal(dataBytes, &data)
		if err != nil {
			fmt.Println(err)
			return
		}
	} else {
		data = Data{
			Wallet:     10000,
			Profit:     0,
			TotalPrice: 10000,
			Coins:      make(map[string]CoinInfo),
		}
	}
	go parser(&data)
	go updateCoins(&data)
	select {}
}
