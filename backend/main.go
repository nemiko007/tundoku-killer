package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

var (
	firebaseApp     *firebase.App     // Firebase Appインスタンスをグローバル変数にする
	firestoreClient *firestore.Client // Firestoreクライアントをグローバル変数にする
)

type LineAuthRequest struct {
	LineAccessToken string `json:"lineAccessToken"`
	LineUserID      string `json:"lineUserID"` // LINE User IDも受け取る
}

// Book は書籍データを表す構造体
type Book struct {
	Title       string    `json:"title" firestore:"title"`
	Author      string    `json:"author" firestore:"author"`
	Deadline    time.Time `json:"deadline" firestore:"deadline"` // time.Time型に変更
	Status      string    `json:"status" firestore:"status"`     // "unread", "reading", "completed"
	InsultLevel int       `json:"insultLevel" firestore:"insultLevel"`
	UserID      string    `json:"userId" firestore:"userId"` // 登録したユーザーのUID
	BookID      string    `json:"bookId" firestore:"bookId"` // FirestoreのドキュメントIDを保存
}

func main() {
	ctx := context.Background()

	// Firebase Admin SDK の初期化
	serviceAccountKeyJSON := os.Getenv("FIREBASE_SERVICE_ACCOUNT_KEY_JSON")
	if serviceAccountKeyJSON == "" {
		log.Fatalf("FIREBASE_SERVICE_ACCOUNT_KEY_JSON environment variable not set")
	}

	opt := option.WithCredentialsJSON([]byte(serviceAccountKeyJSON))
	var err error
	firebaseApp, err = firebase.NewApp(ctx, nil, opt) // グローバル変数に代入
	if err != nil {
		log.Fatalf("error initializing app: %v", err)
	}

	// Firestore クライアントの取得
	firestoreClient, err = firebaseApp.Firestore(ctx)
	if err != nil {
		log.Fatalf("error getting Firestore client: %v", err)
	}
	defer firestoreClient.Close() // アプリ終了時にクライアントをクローズ

	http.HandleFunc("/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from Backend!")
	}))

	http.HandleFunc("/health", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}))

	// LINE認証エンドポイントの追加
	http.HandleFunc("/api/auth/line", corsMiddleware(handleLineAuth))

	// 書籍登録エンドポイントの追加
	http.HandleFunc("/api/books", corsMiddleware(handleRegisterBook))

	// GitHub Actionsからの定期実行用エンドポイント (Cron)
	http.HandleFunc("/api/cron/check", corsMiddleware(handleCheckDeadlines))

	fmt.Println("Server starting on port 8081...")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

// corsMiddleware はCORSヘッダーを追加するミドルウェア
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// すべてのオリジンからのリクエストを許可 (開発用)
		// 本番環境では特定のオリジンに制限することを推奨
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		// プリフライトリクエスト (OPTIONS) の処理
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// handleLineAuth はLINEアクセストークンを受け取り、Firebase Custom Tokenを発行する
func handleLineAuth(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Authクライアントの取得
	client, err := firebaseApp.Auth(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("error getting Auth client: %v", err), http.StatusInternalServerError)
		return
	}

	// リクエストボディのパース
	var req LineAuthRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("error reading request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("error unmarshalling request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.LineAccessToken == "" || req.LineUserID == "" {
		http.Error(w, "lineAccessToken and lineUserID are required", http.StatusBadRequest)
		return
	}

	// ここでLINEアクセストークンの検証を行う (今回はモック)

	// Firebase Custom Token の生成
	// FirebaseのUIDにはLINE User IDを使用する
	customToken, err := client.CustomToken(ctx, req.LineUserID)
	if err != nil {
		http.Error(w, fmt.Sprintf("error creating custom token: %v", err), http.StatusInternalServerError)
		return
	}

	// カスタムトークンをJSON形式で返す
	log.Printf("Generated custom token: %s", customToken)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"customToken": customToken})
}

// handleRegisterBook は書籍登録リクエストを処理する
func handleRegisterBook(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// リクエストボディのパース
	var book Book
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("error reading request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &book); err != nil {
		http.Error(w, fmt.Sprintf("error unmarshalling request body: %v", err), http.StatusBadRequest)
		return
	}

	// 必須フィールドのチェック
	if book.Title == "" || book.Author == "" || book.Deadline.IsZero() || book.UserID == "" {
		http.Error(w, "title, author, deadline, and userId are required", http.StatusBadRequest)
		return
	}
	// デフォルト値を設定
	if book.Status == "" {
		book.Status = "unread"
	}

	// Firestoreに書籍データを保存
	docRef, _, err := firestoreClient.Collection("books").Add(ctx, book)
	if err != nil {
		http.Error(w, fmt.Sprintf("error saving book to Firestore: %v", err), http.StatusInternalServerError)
		return
	}

	// 保存した書籍のFirestoreドキュメントIDをBook構造体に設定
	book.BookID = docRef.ID
	_, err = docRef.Set(ctx, book) // ドキュメントIDをフィールドとして更新
	if err != nil {
		log.Printf("Error updating book with BookID: %v", err)
		http.Error(w, fmt.Sprintf("error updating book with BookID: %v", err), http.StatusInternalServerError)
		return
	}

	// Upstashへのスケジュール登録処理は削除 (GitHub ActionsのCronで定期チェックするため)
	log.Printf("Book registered: %s (Deadline: %v)", book.Title, book.Deadline)

	// 成功レスポンスを返す
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Book registered successfully", "bookId": book.BookID})
}

// handleCheckDeadlines は定期的に実行され、期限切れの未読本をチェックする
func handleCheckDeadlines(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// 簡易的な認証: 環境変数 CRON_SECRET と一致するか確認
	cronSecret := os.Getenv("CRON_SECRET")
	authHeader := r.Header.Get("Authorization")
	if cronSecret != "" && authHeader != "Bearer "+cronSecret {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Firestoreから "unread" の本を取得
	// 複合インデックスを避けるため、まずはステータスでフィルタし、期限はアプリ側でチェックする
	iter := firestoreClient.Collection("books").Where("status", "==", "unread").Documents(ctx)
	defer iter.Stop()

	count := 0
	for {
		doc, err := iter.Next()
		if err == io.EOF || (err != nil && err.Error() == "no more items in iterator") {
			break
		}
		if err != nil {
			log.Printf("Error iterating documents: %v", err)
			http.Error(w, fmt.Sprintf("Error querying database: %v", err), http.StatusInternalServerError)
			return
		}

		var book Book
		if err := doc.DataTo(&book); err != nil {
			log.Printf("Error parsing book data: %v", err)
			continue
		}

		// 期限切れチェック
		if book.Deadline.Before(time.Now()) {
			log.Printf("Found expired book: %s (ID: %s, User: %s, InsultLevel: %d)", book.Title, book.BookID, book.UserID, book.InsultLevel)
			count++

			// 1. Gemini APIを叩いて煽り文を生成
			insultMsg, err := generateInsult(book)
			if err != nil {
				log.Printf("Error generating insult for book %s: %v", book.BookID, err)
				continue
			}

			// 2. LINE Messaging APIでユーザーにメッセージを送信
			if err := sendLineMessage(book.UserID, insultMsg); err != nil {
				log.Printf("Error sending LINE message to user %s: %v", book.UserID, err)
				continue
			}

			// 3. Firestoreの書籍ステータスを更新 (例: "insulted")
			_, err = doc.Ref.Update(ctx, []firestore.Update{
				{Path: "status", Value: "insulted"},
			})
			if err != nil {
				log.Printf("Error updating status for book %s: %v", book.BookID, err)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Checked deadlines. Found %d expired books.", count)})
}

// generateInsult はGemini APIを呼び出して煽り文を生成する
func generateInsult(book Book) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY is not set")
	}

	url := "https://generativelanguage.googleapis.com/v1/models/gemini-pro:generateContent?key=" + apiKey

	prompt := fmt.Sprintf("積読本「%s」(著者: %s) の期限が切れました。罵倒レベル%d (最大5) で、早く読むように煽るメッセージを短く(50文字以内)作成してください。返答はメッセージ内容のみにしてください。", book.Title, book.Author, book.InsultLevel)

	requestBody, _ := json.Marshal(map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"parts": []interface{}{
					map[string]interface{}{
						"text": prompt,
					},
				},
			},
		},
	})

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API error: %s", string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}

	return "早く読みなさい！(Geminiからの応答なし)", nil
}

// sendLineMessage はLINE Messaging API (Push Message) を呼び出す
func sendLineMessage(lineUserID, message string) error {
	accessToken := os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")
	if accessToken == "" {
		return fmt.Errorf("LINE_CHANNEL_ACCESS_TOKEN is not set")
	}

	url := "https://api.line.me/v2/bot/message/push"

	requestBody, _ := json.Marshal(map[string]interface{}{
		"to": lineUserID,
		"messages": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": message,
			},
		},
	})

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LINE API error: %s", string(body))
	}

	return nil
}
