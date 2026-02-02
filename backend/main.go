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

	// 乱数のシードを初期化 (アプリケーション起動時に1回だけ行う)
	rand.Seed(time.Now().UnixNano())

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
	case http.MethodPut:
		handleUpdateBook(w, r)
	case http.MethodDelete:
		handleDeleteBook(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUpdateBook は書籍情報を更新する
func handleUpdateBook(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var book Book
	if err := json.NewDecoder(r.Body).Decode(&book); err != nil {
		http.Error(w, fmt.Sprintf("error decoding request body: %v", err), http.StatusBadRequest)
		return
	}

	if book.BookID == "" || book.UserID == "" {
		http.Error(w, "bookId and userId are required", http.StatusBadRequest)
		return
	}

	// Firestoreのドキュメントを更新
	docRef := firestoreClient.Collection("books").Doc(book.BookID)

	// 更新前にその本の所持者かチェックする（簡易セキュリティ）
	doc, err := docRef.Get(ctx)
	if err != nil {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}
	var existingBook Book
	if err := doc.DataTo(&existingBook); err != nil {
		http.Error(w, "Failed to parse existing book data", http.StatusInternalServerError)
		return
	}
	if existingBook.UserID != book.UserID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_, err = docRef.Set(ctx, book) // 全て上書き
	if err != nil {
		http.Error(w, fmt.Sprintf("error updating book in Firestore: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Book updated: %s (ID: %s)", book.Title, book.BookID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Book updated successfully"})
}

// handleDeleteBook は書籍を削除する
func handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var reqBody struct {
		BookID string `json:"bookId"`
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, fmt.Sprintf("error decoding request body: %v", err), http.StatusBadRequest)
		return
	}

	if reqBody.BookID == "" || reqBody.UserID == "" {
		http.Error(w, "bookId and userId are required", http.StatusBadRequest)
		return
	}

	docRef := firestoreClient.Collection("books").Doc(reqBody.BookID)

	// 削除前に所持者チェック
	doc, err := docRef.Get(ctx)
	if err != nil {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}
	var existingBook Book
	if err := doc.DataTo(&existingBook); err != nil {
		http.Error(w, "Failed to parse existing book data", http.StatusInternalServerError)
		return
	}
	if existingBook.UserID != reqBody.UserID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_, err = docRef.Delete(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("error deleting book from Firestore: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Book deleted: %s", reqBody.BookID)
	w.Header().Set("Content-Type", "application/json")
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

	// Firestoreから "unread" または "insulted" の本を取得
	// 複合インデックスを避けるため、まずはステータスでフィルタし、期限はアプリ側でチェックする
	iter := firestoreClient.Collection("books").Where("status", "in", []string{"unread", "insulted"}).Documents(ctx)
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
	insultMessages := []string{
		"その本、まだ読んでないんですか？時間の無駄ですね。",
		"積読ですか。残念ですね。その本は二度と読まれないでしょう。",
		"買った時の記憶も薄れていくでしょうね。それがあなたの本の末路です。",
		"知識は鮮度が命。その本はもう腐っています。",
		"あなたの読書計画、破綻していますね。",
		fmt.Sprintf("「%s」を読むというタスクは、あなたの優先順位リストに存在しないようですね。", book.Title),
		"無駄な購入でしたね。次からは計画的にどうぞ。",
		"その本は、あなたの怠惰を象徴しています。",
		"期待外れです。次に期待しましょう。",
		"結局、読まない本でしたか。",
		"本棚の肥やしにするために働いてるの？ 貴族か何かですか？",
		"「いつか読む」という言葉、あなたの辞書では「一生読まない」と同じ意味ですよね。",
		"その本の著者が知ったら、絶望して筆を折るレベルの放置っぷりですね。",
		"ページを開く筋肉すら衰えたんですか？ リハビリに1ページどうです？",
		"知識の貯金をしてるつもり？ 複利じゃなくて腐敗が進んでますよ。",
		"本を買うことで満足するタイプですか。安上がりな達成感ですね。",
		"その本、メルカリに出したほうが必要な人の元へ届くし、本も幸せですよ。",
		"次に新しい本を買う前に、その可哀想な既刊を供養してあげたらどうです？",
		fmt.Sprintf("「%s」が放つ『読んでくれオーラ』。鈍感なあなたには届かないようですね。", book.Title),
		"積読は病だと言いますが、あなたはもう手遅れのステージに入っています。",
		"読まない本に囲まれて眠る気分はどうですか？ 知識の亡霊にうなされそうですが。",
		"本の背表紙が寂しそうですよ。たまには視線を合わせてあげたら？",
		"読了できない言い訳を考える時間があるなら、目次くらい読めるでしょうに。",
		"あなたの本棚、もはや墓場ですね。未完の志が眠る場所。",
		"積むのは本じゃなくて、あなたの読書能力にすべきでしたね。",
		"本を買うエネルギーを、読むエネルギーに1%%でも回せませんか？",
		"素晴らしい！ 本の劣化具合を観察する研究でもしてるんですか？",
		"その一冊を無視し続ける胆力、別のことに活かせば成功したでしょうね。",
		fmt.Sprintf("「%s」は、あなたが賢くなるのをずっと、ずっと、無駄に待っていますよ。", book.Title),
		"本を買うお金があるなら、その怠惰を治す薬でも買えばよかったのに。",
		"読みもしない本に場所代を払うなんて、あなたは本棚の大家さんですか？",
		"そろそろ、その本にカビが生えるか、あなたの脳にカビが生えるかの勝負ですね。",
		"文字を追うのがそれほど苦痛なら、いっそ絵本からやり直しますか？",
		"その本、もうあなたの記憶からは消去されてるんでしょうね。物理的にあるだけで。",
		"読書家を自称してるなら、死ぬ気でその一冊を終わらせるべきじゃないですか？",
		"あなたの「忙しい」は、本にとって「お前はどうでもいい」という死刑宣告ですよ。",
		"本棚が重みに耐えかねています。あなたの怠慢の重みに、ですよ。",
		"未読のまま古びていく本。まるであなたの知性の成長が止まったかのようですね。",
		"ページをめくる心地よさ。あ、忘れてしまったんでしたっけ？",
		"その本の内容、SNSで誰かが要約してくれるのを待ってるんですか？ 浅ましいですね。",
		"紙の無駄。インクの無駄。そして、あなたの時間の無駄。",
		"もしかして、枕として使ってるんですか？ 知識が染み込むといいですね（笑）",
		"その本、あなたの何倍も賢い内容が詰まってるのに、宝の持ち腐れですね。",
		"読まない権利を行使中ですか？ 憲法にでも書いてありましたっけ？",
		"「読みたい」という言葉は、実行が伴って初めて意味を成すんですよ。ご存知？",
		fmt.Sprintf("「%s」の続き、気にならないんですか？ あなたの人生と同じで、停滞していますね。", book.Title),
		"本は読まれるために生まれてきたんです。あなたの見栄のためにあるんじゃない。",
		"読まない本を積み上げるのは、読書ではなく単なる『物流』ですよ。",
		"あなたの怠慢は、出版業界に対する静かなテロリズムですね。",
		"その本、あと10年経っても同じ場所にありそうですね。化石かな？",
		"知的な刺激に飢えていると言いつつ、目の前の御馳走を放置する。矛盾の塊ですね。",
		"ページを開く。たったそれだけのことが、今のあなたにはエベレスト登頂並みに困難なようで。",
		"本を買った自分を褒めて終わりですか？ 達成感のコストパフォーマンス、良すぎません？",
		"その本の存在を忘れていた自分を、まずは恥じるべきではないでしょうか。",
		"あなたが読まない間に、世界はその本から知識を得て、あなたを追い抜いていきますよ。",
		"本は友達？ ならば、あなたは友人を放置して放置して、見捨てている加害者ですね。",
		"読書、義務じゃないけど、教養は義務ですよ。その本はその欠片だったはず。捨てたんですか？",
		"本の死は、読まれなくなること。あなたは今、一冊の本を殺そうとしています。",
		"積読を肯定する文化に逃げないでください。あなたはただ読まないだけです。",
		fmt.Sprintf("「%s」の背表紙の色褪せ。あなたの情熱の色褪せそのものですね。", book.Title),
		"買って満足、積んで満足。読書家ごっこ、楽しそうで何よりです。",
		"その本を一気に読める集中力、どこかに落としてきたんですか？",
		"読まない理由を100個並べるより、1ページめくるほうが生産的ですよ。",
		"本棚の容量にも限界があるように、あなたの怠慢を受け入れられる器にも限界があります。",
		"明日から読む？ その『明日』は、365回くらい通り過ぎましたよね？",
		"本を読むことは呼吸と同じだと言った人がいますが、あなたは窒息死寸前ですね。",
		"その本を手に取る勇気。今のあなたには、何よりも欠けているもののようです。",
		"知識の倉庫番。それがあなたの現在の職業ですか？ 給料、出ませんよ。",
		"本がかわいそうです。せめて、他の方に譲るという慈悲の心は持てないのですか？",
		"積み上げられた本は、あなたの怠けた日々のチェックポイントですね。",
		"本を読まない理由が「時間がない」？ そのスマホを触る指をページに置けと言ってるんです。",
		"あなたの本棚、湿度高そうですね。未読本の涙で。",
		"その一冊、読み終えたら新しい世界が見えるかもしれないのに。一生盲目のままですか？",
		"本を買うことで自分をアップデートした気にならないでください。中身は空っぽのままですよ。",
		"その本、最後に触ったのいつですか？ 埃が厚化粧のように積もっていますよ。",
		"他人の書評で読んだ気になっていませんか？ 自分の頭で考えない読書家（笑）ですね。",
		"本の価値を紙の重さだと思っていませんか？ 中にある『言葉』を殺さないでください。",
		fmt.Sprintf("「%s」というタイトル、今のあなたの心には全く響いていないようですね。", book.Title),
		"積読を『楽しみ』だと強弁する。負け惜しみの定義として辞典に載せたいくらいです。",
		"あなたの読書スピード、亀より遅い…あ、そもそも動いてすらいませんでしたね。",
		"文字を読むことが、それほどまでにあなたの高いプライドに障りますか？",
		"本は鏡です。あなたの今の怠惰な姿を、その未読のページが映し出していますよ。",
		"いつか役に立つ？ その『いつか』が来たとき、あなたは内容を全く知らないことに絶望するでしょう。",
		"その本が可哀想で見ていられません。私が代わりに読んであげましょうか？ （冗談です、あなたの本ですから）",
		"教養の壁を積み上げているつもりでしょうが、それは単なる『無知の檻』です。",
		"読書を後回しにする。つまり、自分自身の成長を後回しにしているということです。",
		"その本、もし喋れたら、あなたに一番に何を言うでしょうね？ 『さよなら』かな？",
		"本の山を眺めて知的な気分に浸る。コスプレとしては安上がりで良いですね。",
		"一冊すら完結できない人間が、人生のチャプターをどう進めるつもりですか？",
		"積読は未来への投資？ 投資なら運用しないとただの『死に金』ですよ。",
		"その本を開く。そんな簡単なことができないあなたに、何ができるというのですか？",
		"もう、その本をメルカリの梱包材にでも使ったらどうです？ 最後の仕事として。",
		"あなたが眠っている間も、その本は「読まれたい」と叫び続けていますよ。聞こえませんか？",
		"結局、あなたは本が好きなのではなく、『本を持っている自分が好き』なだけですね。",
	}
	randomIndex := rand.Intn(len(insultMessages)) // グローバルのrandを使用

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
