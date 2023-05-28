package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	SlackApiBaseUrl = "https://slack.com/api/"
	ChatGptApiUrl   = "https://api.openai.com/v1/chat/completions"
	AnswerLimit     = 10
)

var slackBotToken string
var chatGptApiKey string

type SlackMessage struct {
	Type       string `json:"type"`
	User       string `json:"user"`
	Text       string `json:"text"`
	Ts         string `json:"ts"`
	ThreadTs   string `json:"thread_ts"`
	ReplyCount int    `json:"reply_count"`
}

type SlackConversationsHistoryResponse struct {
	Ok       bool           `json:"ok"`
	Messages []SlackMessage `json:"messages"`
	Error    string         `json:"error"`
	Needed   string         `json:"needed"`
}

type SlackPostMessageResponse struct {
	Ok     bool   `json:"ok"`
	Error  string `json:"error"`
	Needed string `json:"needed"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatGPTPayLoad struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type ChatGptResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func init() {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println("Error loading .env file")
		return
	}
}

func main() {
	slackBotToken = os.Getenv("SLACK_BOT_TOKEN")
	chatGptApiKey = os.Getenv("CHAT_GPT_API_KEY")
	channelId := os.Getenv("SLACK_CHANNEL_ID")

	messages, err := fetchSlackMessages(channelId)
	if err != nil {
		fmt.Println("Error fetching slack message:", err)
		return
	}

	sort.Slice(messages, func(i, j int) bool {
		tsi, err := strconv.ParseFloat(messages[i].Ts, 64)
		if err != nil {
			return false
		}

		tsj, err := strconv.ParseFloat(messages[j].Ts, 64)
		if err != nil {
			return false
		}

		return tsi < tsj
	})

	var filterMessages []SlackMessage
	for _, message := range messages {
		if isQuestion(message.Text) && message.ReplyCount == 0 {
			filterMessages = append(filterMessages, message)
		}
	}

	for i, message := range filterMessages {
		time.Sleep(time.Second * 60)
		if i > AnswerLimit {
			break
		}

		resp, err := sendToChatGpt(message.Text)
		if err != nil {
			fmt.Println("Error sending message to ChatGPT:", err)
			continue
		}

		respWithMention := fmt.Sprintf("<@%s>\n%s", message.User, resp)
		err = postToSlackThread(channelId, message.ThreadTs, respWithMention)
		if err != nil {
			fmt.Println("Error posting to Slack thread:", err)
			continue
		}

		fmt.Println("Post Slack Thread Done")
	}
}

func fetchSlackMessages(channelId string) ([]SlackMessage, error) {
	now := time.Now()
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return nil, err
	}
	yesterday := now.AddDate(0, 0, -1)
	startTime := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 20, 0, 0, 0, jst)
	url := fmt.Sprintf("%sconversations.history?channel=%s&oldest=%d", SlackApiBaseUrl, channelId, startTime.Unix())

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", slackBotToken))

	client := &http.Client{Timeout: time.Second * 10}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResponse SlackConversationsHistoryResponse
	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return nil, err
	}

	if !apiResponse.Ok {
		return nil, fmt.Errorf("slack API error: %s, needed: %s", apiResponse.Error, apiResponse.Needed)
	}

	return apiResponse.Messages, nil
}

func isQuestion(s string) bool {
	return strings.Contains(s, "質問です")
}

func postToSlackThread(channelId, threadTs, message string) error {
	url := fmt.Sprintf("%schat.postMessage", SlackApiBaseUrl)

	requestData := map[string]interface{}{
		"token":     slackBotToken,
		"channel":   channelId,
		"text":      message,
		"thread_ts": threadTs,
	}

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", slackBotToken))

	client := &http.Client{Timeout: time.Second * 10}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var apiResponse SlackPostMessageResponse
	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return err
	}

	if !apiResponse.Ok {
		return fmt.Errorf("slack API error: %s, needed: %s", apiResponse.Error, apiResponse.Needed)
	}

	return nil
}

func sendToChatGpt(prompt string) (string, error) {
	message := []ChatMessage{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	requestData := ChatGPTPayLoad{
		Model:    "gpt-3.5-turbo",
		Messages: message,
	}

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", ChatGptApiUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", chatGptApiKey))

	client := &http.Client{Timeout: time.Minute * 15}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var apiResponse ChatGptResponse

	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return "", err
	}

	if len(apiResponse.Choices) == 0 {
		return "APIからのレスポンスがありませんでした。APIのレート制限にひっかかった可能性がありんす。", nil
	}

	return apiResponse.Choices[0].Message.Content, nil
}
