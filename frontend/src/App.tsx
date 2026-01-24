import { useState, useEffect } from "react";
import type { FormEvent } from "react";
import liff from "@line/liff"; // LIFFをインポート
import { signInWithCustomToken } from "firebase/auth";
import { doc, setDoc } from "firebase/firestore";
import { auth, db } from "./firebase"; // Firebaseの初期化ファイルをインポート

interface LineUserProfile {
    userId: string;
    displayName: string;
    pictureUrl?: string;
    statusMessage?: string;
}

interface Book {
    title: string;
    author: string;
    deadline: string; // ISO String
    status: string;
    insultLevel: number;
    userId: string;
    bookId: string;
}

function App() {
    const [isLoggedIn, setIsLoggedIn] = useState(false);
    const [lineProfile, setLineProfile] = useState<LineUserProfile | null>(
        null,
    );
    const [firebaseUser, setFirebaseUser] = useState<any>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [books, setBooks] = useState<Book[]>([]); // 書籍リスト用のstate

    // 書籍登録フォームの状態管理
    const [title, setTitle] = useState("");
    const [author, setAuthor] = useState("");
    const [deadline, setDeadline] = useState(""); // YYYY-MM-DD 形式を想定
    const [insultLevel, setInsultLevel] = useState(3); // デフォルトを3に設定

    useEffect(() => {
        const initializeLiffAndLogin = async () => {
            try {
                if (!liff.isLoggedIn()) {
                    // LIFFにログインしていない場合はログイン画面へ
                    liff.login();
                    return; // ログイン処理でページ遷移するのでここで終了
                }

                setIsLoggedIn(true);

                // LIFFからアクセストークンとプロフィールを取得
                const lineAccessToken = liff.getAccessToken();
                const profile = await liff.getProfile();
                setLineProfile(profile);

                if (!lineAccessToken || !profile.userId) {
                    setError("Failed to get LINE access token or user ID.");
                    setLoading(false);
                    return;
                }

                // バックエンドにアクセストークンを送ってFirebase Custom Tokenを取得
                const response = await fetch(
                    "https://tundoku-killer.onrender.com/api/auth/line",
                    {
                        method: "POST",
                        headers: {
                            "Content-Type": "application/json",
                        },
                        body: JSON.stringify({
                            lineAccessToken: lineAccessToken,
                            lineUserID: profile.userId,
                        }),
                    },
                );

                if (!response.ok) {
                    throw new Error(
                        "Failed to get Firebase Custom Token from backend.",
                    );
                }

                const data = await response.json();
                const customToken = data.customToken;

                // Firebase Custom TokenでFirebaseにサインイン
                const userCredential = await signInWithCustomToken(
                    auth,
                    customToken,
                );
                setFirebaseUser(userCredential.user);

                // ユーザー情報をFirestoreに保存または更新
                const userRef = doc(db, "users", userCredential.user.uid);
                await setDoc(
                    userRef,
                    {
                        displayName: profile.displayName,
                        lineUserId: profile.userId,
                        // 必要に応じて他のLINEプロフィール情報も保存
                    },
                    { merge: true },
                ); // 既存のフィールドは上書きせずマージ

                // 登録済みの書籍リストを取得
                const booksResponse = await fetch(
                    `https://tundoku-killer.onrender.com/api/books?userId=${userCredential.user.uid}`,
                );
                if (!booksResponse.ok) {
                    const errorBody = await booksResponse.text();
                    const errorMessage = `Failed to fetch books. Status: ${booksResponse.status}. Body: ${errorBody}`;
                    console.error(errorMessage);
                    throw new Error(errorMessage); // catchブロックでsetErrorに渡す
                }
                const booksData = await booksResponse.json();
                console.log("Fetched books:", booksData);
                setBooks(booksData || []); // データがnullの場合も考慮して空配列をセット
            } catch (err: any) {
                console.error("LIFF/Firebase login error:", err);
                setError(
                    err.message || "An unexpected error occurred during login.",
                );
            } finally {
                setLoading(false);
            }
        };

        initializeLiffAndLogin();
    }, []); // 最初のレンダリング時に一度だけ実行

    const handleSubmit = async (e: FormEvent) => {
        e.preventDefault();
        setLoading(true);
        setError(null);

        if (!firebaseUser?.uid) {
            setError("Firebase user not logged in.");
            setLoading(false);
            return;
        }

        try {
            const bookData = {
                title,
                author,
                deadline: new Date(deadline).toISOString(), // ISO 8601形式に変換
                insultLevel: Number(insultLevel),
                userId: firebaseUser.uid,
            };

            const response = await fetch(
                "https://tundoku-killer.onrender.com/api/books",
                {
                    method: "POST",
                    headers: {
                        "Content-Type": "application/json",
                    },
                    body: JSON.stringify(bookData),
                },
            );

            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(
                    errorData.message || "書籍登録に失敗しました。",
                );
            }

            const result = await response.json();
            alert(result.message || "書籍を登録しました！");

            // フロントのstateも更新して即時反映
            // bookDataにはdeadlineがISO文字列で入っているが、フォームのstateは 'YYYY-MM-DD' 形式
            // 表示と内部データ形式を合わせるため、ここで再構築
            const newBook: Book = {
                title: title,
                author: author,
                deadline: new Date(deadline).toISOString(),
                status: "unread",
                insultLevel: Number(insultLevel),
                userId: firebaseUser.uid,
                bookId: result.bookId, // バックエンドから返されたbookId
            };
            setBooks((prevBooks) => [...prevBooks, newBook]);
            console.log("Registered bookId:", result.bookId);

            // フォームをクリア
            setTitle("");
            setAuthor("");
            setDeadline("");
            setInsultLevel(3);
        } catch (err: any) {
            console.error("書籍登録エラー:", err);
            setError(
                err.message || "書籍登録中に予期せぬエラーが発生しました。",
            );
        } finally {
            setLoading(false);
        }
    };

    const handleCompleteClick = async (bookId: string) => {
        try {
            const response = await fetch(
                "https://tundoku-killer.onrender.com/api/books/complete",
                {
                    method: "POST",
                    headers: {
                        "Content-Type": "application/json",
                    },
                    body: JSON.stringify({ bookId }),
                },
            );

            if (!response.ok) {
                const errorBody = await response.text();
                const errorMessage = `Failed to mark book as completed. Status: ${response.status}. Body: ${errorBody}`;
                console.error(errorMessage);
                throw new Error(errorMessage);
            }

            // UIの書籍ステータスを更新
            setBooks((prevBooks) =>
                prevBooks.map((book) =>
                    book.bookId === bookId ? { ...book, status: "completed" } : book
                )
            );
        } catch (err: any) {
            console.error("読了処理エラー:", err);
            setError(
                err.message || "読了処理中に予期せぬエラーが発生しました。",
            );
        }
    };

    if (loading) {
        return (
            <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-pink-400 via-purple-500 to-indigo-600 text-white text-3xl font-bold animate-pulse">
                💖 Loading... 💖
            </div>
        );
    }

    if (error) {
        return (
            <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-red-500 to-pink-500 text-white text-2xl font-bold p-4 text-center">
                ぴえん🥺！エラーだよ！💦: {error}
            </div>
        );
    }

    const completedBooks = books.filter((book) => book.status === "completed");
    const unreadBooks = books.filter((book) => book.status !== "completed");

    return (
        <div className="min-h-screen flex flex-col items-center justify-center p-4 bg-gradient-to-br from-pink-400 via-purple-500 to-indigo-600 text-white">
            <h1 className="text-5xl md:text-6xl font-black text-transparent bg-clip-text bg-gradient-to-r from-yellow-300 via-pink-400 to-purple-500 mb-8 drop-shadow-lg animate-pulse">
                ツンドク・キラー🔥
            </h1>

            {isLoggedIn && firebaseUser ? (
                <div className="bg-pink-700 p-8 rounded-xl shadow-lg drop-shadow-md w-full max-w-md border-2 border-pink-300 transform transition-transform duration-300 hover:scale-105" style={{ boxShadow: '0 0 10px #ff00ff, 0 0 20px #ff00ff, 0 0 30px #ff00ff' }}>
                    <p className="text-2xl font-black text-pink-200 mb-4 text-center drop-shadow-md">
                        💖ようこそ、{lineProfile?.displayName}さま！💖
                    </p>
                    {lineProfile?.pictureUrl && (
                        <img
                            src={lineProfile.pictureUrl}
                            alt="Profile"
                            className="w-28 h-28 rounded-full mx-auto mb-5 border-4 border-pink-300 shadow-md transform transition-transform duration-300 hover:scale-110"
                        />
                    )}
                    <p className="text-purple-200 text-sm mb-2 text-center">
                        キミのFirebase UID: <span className="font-bold text-pink-100">{firebaseUser.uid}</span>
                    </p>
                    <p className="text-purple-300 text-sm mb-6 text-center">
                        LIFFログインとFirebase連携、完璧じゃん！天才！✌️
                    </p>

                    <h2 className="text-3xl font-black text-pink-200 mb-6 text-center drop-shadow-md">
                        💖書籍を登録するしかなくない？💖
                    </h2>
                    <form onSubmit={handleSubmit} className="space-y-5">
                        <div>
                            <label
                                htmlFor="title"
                                className="block text-pink-100 text-base font-bold mb-2 drop-shadow-sm"
                            >
                                タイトル:
                            </label>
                            <input
                                type="text"
                                id="title"
                                value={title}
                                onChange={(e) => setTitle(e.target.value)}
                                className="shadow-lg appearance-none border-2 border-pink-300 rounded-lg w-full py-3 px-4 text-gray-800 leading-tight focus:outline-none focus:ring-2 focus:ring-pink-400 focus:border-transparent transition-all duration-200"
                                required
                            />
                        </div>
                        <div>
                            <label
                                htmlFor="author"
                                className="block text-pink-100 text-base font-bold mb-2 drop-shadow-sm"
                            >
                                著者:
                            </label>
                            <input
                                type="text"
                                id="author"
                                value={author}
                                onChange={(e) => setAuthor(e.target.value)}
                                className="shadow-lg appearance-none border-2 border-pink-300 rounded-lg w-full py-3 px-4 text-gray-800 leading-tight focus:outline-none focus:ring-2 focus:ring-pink-400 focus:border-transparent transition-all duration-200"
                                required
                            />
                        </div>
                        <div>
                            <label
                                htmlFor="deadline"
                                className="block text-pink-100 text-base font-bold mb-2 drop-shadow-sm"
                            >
                                読了期限:
                            </label>
                            <input
                                type="date"
                                id="deadline"
                                value={deadline}
                                onChange={(e) => setDeadline(e.target.value)}
                                className="shadow-lg appearance-none border-2 border-pink-300 rounded-lg w-full py-3 px-4 text-gray-800 leading-tight focus:outline-none focus:ring-2 focus:ring-pink-400 focus:border-transparent transition-all duration-200"
                                required
                            />
                        </div>
                        <div>
                            <label
                                htmlFor="insultLevel"
                                className="block text-pink-100 text-base font-bold mb-2 drop-shadow-sm"
                            >
                                煽りレベル:
                            </label>
                            <select
                                id="insultLevel"
                                value={insultLevel}
                                onChange={(e) =>
                                    setInsultLevel(Number(e.target.value))
                                }
                                className="shadow-lg border-2 border-pink-300 rounded-lg w-full py-3 px-4 text-gray-800 leading-tight focus:outline-none focus:ring-2 focus:ring-pink-400 focus:border-transparent transition-all duration-200"
                            >
                                <option value={1}>1 (やさしく)</option>
                                <option value={2}>2 (ちょっと煽る)</option>
                                <option value={3}>3 (普通に煽る)</option>
                                <option value={4}>4 (かなり煽る)</option>
                                <option value={5}>5 (鬼煽り！)</option>
                            </select>
                        </div>
                        <button
                            type="submit"
                            className="bg-gradient-to-r from-yellow-400 to-orange-500 hover:from-yellow-300 hover:to-orange-400 text-white font-black py-3 px-6 rounded-full w-full focus:outline-none focus:shadow-outline transform transition-transform duration-300 hover:scale-105 text-lg shadow-xl uppercase tracking-wider"
                        >
                            💖書籍を登録するしかなくない？！💖
                        </button>
                    </form>

                    <div className="mt-10 p-6 bg-pink-700 rounded-xl shadow-lg drop-shadow-md border-2 border-pink-300" style={{ boxShadow: '0 0 10px #ff00ff, 0 0 20px #ff00ff, 0 0 30px #ff00ff' }}>
                        <h2 className="text-3xl font-black text-pink-200 mb-6 text-center drop-shadow-md">
                            💖未読・読書中の本💖
                        </h2>
                        {unreadBooks.length > 0 ? (
                            <ul className="space-y-6">
                                {unreadBooks.map((book) => (
                                    <li
                                        key={book.bookId}
                                        className="bg-purple-800 p-5 rounded-lg shadow-lg border-2 border-purple-400 transform transition-transform duration-300"
                                    >
                                        <h3 className="text-xl font-black text-yellow-300 mb-1">
                                            {book.title}
                                        </h3>
                                        <p className="text-pink-100 text-sm">
                                            著者: {book.author}
                                        </p>
                                        <p className="text-purple-200 text-xs mt-1">
                                            期限:{" "}
                                            {new Date(
                                                book.deadline,
                                            ).toLocaleDateString()}
                                            {book.status !== "completed" && new Date(book.deadline) < new Date() && (
                                                <span className="ml-2 text-red-400 font-bold">期限切れ！💦</span>
                                            )}
                                        </p>
                                        <p
                                            className={`text-sm font-black mt-2 uppercase ${
                                                book.status === "insulted"
                                                    ? "text-red-400 animate-pulse"
                                                    : book.status ===
                                                        "completed"
                                                      ? "text-green-300"
                                                      : "text-yellow-300"
                                            }`}
                                        >
                                            ステータス: {book.status === "unread" ? "未読" : book.status === "reading" ? "読書中" : book.status === "completed" ? "読了済" : "煽られ中"}
                                        </p>
                                        {book.status !== "completed" && (
                                            <button
                                                onClick={() =>
                                                    handleCompleteClick(
                                                        book.bookId,
                                                    )
                                                }
                                                className="mt-4 bg-gradient-to-r from-green-400 to-blue-500 hover:from-green-300 hover:to-blue-400 text-white font-black py-2 px-4 rounded-full text-sm focus:outline-none focus:shadow-outline transform transition-transform duration-300 hover:scale-110 shadow-md"
                                            >
                                                読了！天才じゃん！✌️
                                            </button>
                                        )}
                                    </li>
                                ))}
                            </ul>
                        ) : (
                            <p className="text-center text-pink-200 mt-4 text-lg font-bold">
                                まだ登録された本はないみたい？🥺 早く登録しよっ！
                            </p>
                        )}
                    </div>

                    <div className="mt-10 p-6 bg-pink-700 rounded-xl shadow-lg drop-shadow-md border-2 border-pink-300" style={{ boxShadow: '0 0 10px #ff00ff, 0 0 20px #ff00ff, 0 0 30px #ff00ff' }}>
                        <h2 className="text-3xl font-black text-pink-200 mb-6 text-center drop-shadow-md">
                            💖読了済みの本💖
                        </h2>
                        {completedBooks.length > 0 ? (
                            <ul className="space-y-6">
                                {completedBooks.map((book) => (
                                    <li
                                        key={book.bookId}
                                        className="bg-green-800 p-5 rounded-lg shadow-lg border-2 border-green-400 transform transition-transform duration-300"
                                    >
                                        <h3 className="text-xl font-black text-yellow-300 mb-1">
                                            {book.title}
                                        </h3>
                                        <p className="text-green-100 text-sm">
                                            著者: {book.author}
                                        </p>
                                        <p className="text-green-200 text-xs mt-1">
                                            読了日:{" "}
                                            {new Date(
                                                book.deadline,
                                            ).toLocaleDateString()}
                                        </p>
                                        <p className="text-sm font-black mt-2 uppercase text-green-300">
                                            ステータス: 読了済！天才！
                                        </p>
                                    </li>
                                ))}
                            </ul>
                        ) : (
                            <p className="text-center text-pink-200 mt-4 text-lg font-bold">
                                まだ読了済みの本はないみたい？🥺
                            </p>
                        )}
                    </div>
                </div>
            ) : (
                <div className="bg-purple-800 p-8 rounded-xl shadow-lg drop-shadow-md text-center border-2 border-purple-300" style={{ boxShadow: '0 0 10px #8a2be2, 0 0 20px #8a2be2, 0 0 30px #8a2be2' }}>
                    <p className="text-xl text-pink-200 mb-4 font-bold animate-pulse">
                        まだLIFFにログインしてないよ〜🥺
                    </p>
                    <button
                        onClick={() => liff.login()}
                        className="bg-pink-500 hover:bg-pink-400 text-white font-bold py-3 px-6 rounded-full focus:outline-none focus:shadow-outline transform transition-transform duration-300 hover:scale-110 shadow-lg text-lg"
                    >
                        LINEでログインするしかなくない？💖
                    </button>
                </div>
            )}
        </div>
    );

}

export default App;
