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
	"strings"
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

// WorkflowPayload はUpstash Workflowから送られてくるペイロードの構造体
type WorkflowPayload struct {
	BookID      string `json:"bookId"`
	UserID      string `json:"userId"`
	InsultLevel int    `json:"insultLevel"`
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

	// Upstash Workflowからの煽り実行エンドポイントの追加
	http.HandleFunc("/api/workflow/execute", corsMiddleware(handleWorkflowExecute))

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

	// Upstash Workflowをキックする
	qstashURL := os.Getenv("QSTASH_URL")
	qstashToken := os.Getenv("QSTASH_TOKEN")
	if qstashURL == "" || qstashToken == "" {
		http.Error(w, "QSTASH_URL and QSTASH_TOKEN environment variables must be set", http.StatusInternalServerError)
		return
	} else {
		// 煽り実行エンドポイントのURL (バックエンドがRenderにデプロイされた場合のURL)
		renderExternalHostname := os.Getenv("RENDER_EXTERNAL_HOSTNAME")
		if renderExternalHostname == "" {
			http.Error(w, "RENDER_EXTERNAL_HOSTNAME environment variable not set for Upstash Workflow callback URL.", http.StatusInternalServerError)
			return
		}
		targetURL := fmt.Sprintf("%s/api/workflow/execute", strings.TrimSuffix(renderExternalHostname, "/"))

		// Upstash Workflowに送るペイロード
		workflowPayload := map[string]string{
			"bookId":      docRef.ID,
			"userId":      book.UserID,
			"insultLevel": fmt.Sprintf("%d", book.InsultLevel),
		}
		jsonPayload, _ := json.Marshal(workflowPayload)

		// 煽り開始までの遅延時間 (ミリ秒)
		// 例: 期限の1日前から煽り始める場合 (ここでは期限時刻に設定)
		delayMs := book.Deadline.UnixMilli() - time.Now().UnixMilli()
		if delayMs < 0 {
			delayMs = 0 // 期限が過ぎている場合はすぐに実行
		}

		// QStashのURLがベースURLのみ(例: https://qstash.upstash.io)の場合に対応
		if !strings.Contains(qstashURL, "/v2/publish") {
			qstashURL = strings.TrimSuffix(qstashURL, "/") + "/v2/publish"
		}

		// QStashへのリクエストURLを作成 (宛先URLをパスに含める)
		// 例: https://qstash.upstash.io/v2/publish/https://my-backend.com/api/workflow/execute
		publishURL := fmt.Sprintf("%s/%s", strings.TrimSuffix(qstashURL, "/"), targetURL)

		log.Printf("Sending Upstash request to: %s", publishURL)
		req, err := http.NewRequestWithContext(ctx, "POST", publishURL, bytes.NewBuffer(jsonPayload))
		if err != nil {
			log.Printf("Error creating Upstash Workflow request: %v", err)
		} else {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+qstashToken)
			req.Header.Set("Upstash-Delay", fmt.Sprintf("%dms", delayMs)) // ms単位で遅延を指定

			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error sending request to Upstash Workflow: %v", err)
			} else {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
					respBody, _ := io.ReadAll(resp.Body)
					log.Printf("Upstash Workflow responded with status %d: %s", resp.StatusCode, string(respBody))
				} else {
					log.Printf("Successfully scheduled Upstash Workflow for book %s", book.Title)
				}
			}
		}
	}

	// 成功レスポンスを返す
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Book registered successfully", "bookId": book.BookID})
}

// handleWorkflowExecute はUpstash Workflowからのリクエストを受け取り、煽りメッセージを生成・送信する
func handleWorkflowExecute(w http.ResponseWriter, r *http.Request) {
	// リクエストボディのパース
	var payload WorkflowPayload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, fmt.Sprintf("Error unmarshalling request body: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("Received workflow execute request for bookId: %s, userId: %s, insultLevel: %d", payload.BookID, payload.UserID, payload.InsultLevel)

	// TODO: ここに煽り実行ロジックを実装
	// 1. Firestoreから書籍情報を取得
	// 2. 書籍が未読の場合のみ処理 (status == "unread")
	// 3. Gemini APIを叩いて煽り文を生成
	// 4. LINE Messaging APIでユーザーにメッセージを送信
	// 5. Firestoreの書籍ステータスを更新 (例: "insulted")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow execute received and processed (mocked)."})
}
