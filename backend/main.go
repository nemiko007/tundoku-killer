package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

		"cloud.google.com/go/firestore"

		"google.golang.org/api/iterator"

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

	// 書籍関連のエンドポイント
	http.HandleFunc("/api/books", corsMiddleware(handleBooks))

	// 読了処理のエンドポイント
	http.HandleFunc("/api/books/complete", corsMiddleware(handleCompleteBook))

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

// handleBooks は /api/books へのリクエストをHTTPメソッドに応じて振り分ける
func handleBooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleGetBooks(w, r)
	case http.MethodPost:
		handleRegisterBook(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetBooks は登録済みの書籍リストを取得する
func handleGetBooks(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	userId := r.URL.Query().Get("userId")

	if userId == "" {
		http.Error(w, "userId query parameter is required", http.StatusBadRequest)
		return
	}

	// Firestoreから "completed" ではない本を取得
	iter := firestoreClient.Collection("books").
		Where("userId", "==", userId).
		// Where("status", "!=", "completed"). // 読了済みの本も一旦すべて取得
		Documents(ctx)
	defer iter.Stop()

	var books []Book
	for {
		doc, err := iter.Next()
		if err == io.EOF || err == iterator.Done { // firestore.Doneも追加でチェック！
			break
		}
		if err != nil {
			log.Printf("Error iterating documents: %v (Type: %T)", err, err) // エラーの型もログに出す！
			http.Error(w, fmt.Sprintf("Failed to retrieve books: %v", err), http.StatusInternalServerError)
			return
		}

		var book Book
		if err := doc.DataTo(&book); err != nil {
			log.Printf("Error parsing book data: %v", err)
			continue
		}
		books = append(books, book)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
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

	// 新しいドキュメント参照を作成し、そのIDをbook.BookIDに設定
	docRef := firestoreClient.Collection("books").NewDoc()
	book.BookID = docRef.ID

	// Book構造体全体をFirestoreに保存
	_, err = docRef.Set(ctx, book)
	if err != nil {
		http.Error(w, fmt.Sprintf("error saving book to Firestore: %v", err), http.StatusInternalServerError)
		return
	}

	// Upstashへのスケジュール登録処理は削除 (GitHub ActionsのCronで定期チェックするため)
	log.Printf("Book registered: %s (Deadline: %v)", book.Title, book.Deadline)

	// 成功レスポンスを返す
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Book registered successfully", "bookId": book.BookID})
}

// handleCompleteBook は書籍のステータスを "completed" に更新する
func handleCompleteBook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	var reqBody struct {
		BookID string `json:"bookId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		bodyBytes, _ := io.ReadAll(r.Body) // Read body again for logging (NewDecoder consumes it)
		http.Error(w, fmt.Sprintf("Invalid request body: %v, received: %s", err, string(bodyBytes)), http.StatusBadRequest)
		return
	}

	if reqBody.BookID == "" {
		log.Printf("BookID is empty in request body for /api/books/complete")
		http.Error(w, "bookId is required", http.StatusBadRequest)
		return
	}

	// 書籍ドキュメントの参照を取得
	docRef := firestoreClient.Collection("books").Doc(reqBody.BookID)

	// ステータスを "completed" に更新
	_, err := docRef.Update(ctx, []firestore.Update{
		{Path: "status", Value: "completed"},
	})

	if err != nil {
		log.Printf("Error updating book status: %v", err)
		http.Error(w, "Failed to update book status", http.StatusInternalServerError)
		return
	}

	log.Printf("Book %s marked as completed.", reqBody.BookID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Book marked as completed"})
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

// generateInsult はあらかじめ用意された煽り文からランダムに1つを返す
func generateInsult(book Book) (string, error) {
	// 乱数のシードを初期化。毎回違う結果を得るために重要。
	rand.New(rand.NewSource(time.Now().UnixNano()))

	insultMessages := []string{
		"その本、いつ読むの？もうオブジェになってない？w",
		"積読タワー建設中？完成披露パーティーはいつですか？（早く読め）",
		"買った時の情熱、どこいった〜？🔥 本が泣いてるよ！",
		fmt.Sprintf("「%s」が本棚の飾りになってるって噂、本当だったんだね…", book.Title),
		"読書、今日からじゃなくて今から始めよっか！",
		"その本、インテリアにするにはちょっと高いんじゃない？笑",
		"大丈夫、まだ間に合う！その本を手に取って最初の1ページを開くだけでいい！",
	}

	// ランダムにメッセージを選択
	randomIndex := rand.Intn(len(insultMessages))

	return insultMessages[randomIndex], nil
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
