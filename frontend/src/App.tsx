import { useState, useEffect } from 'react';
import type { FormEvent } from 'react';
import liff from '@line/liff'; // LIFFをインポート
import { signInWithCustomToken } from 'firebase/auth';
import { doc, setDoc } from 'firebase/firestore';
import { auth, db } from './firebase'; // Firebaseの初期化ファイルをインポート

interface LineUserProfile {
  userId: string;
  displayName: string;
  pictureUrl?: string;
  statusMessage?: string;
}

function App() {
  const [isLoggedIn, setIsLoggedIn] = useState(false);
  const [lineProfile, setLineProfile] = useState<LineUserProfile | null>(null);
  const [firebaseUser, setFirebaseUser] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // 書籍登録フォームの状態管理
  const [title, setTitle] = useState('');
  const [author, setAuthor] = useState('');
  const [deadline, setDeadline] = useState(''); // YYYY-MM-DD 形式を想定
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
          setError('Failed to get LINE access token or user ID.');
          setLoading(false);
          return;
        }

        // バックエンドにアクセストークンを送ってFirebase Custom Tokenを取得
        const response = await fetch('http://localhost:8081/api/auth/line', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            lineAccessToken: lineAccessToken,
            lineUserID: profile.userId,
          }),
        });

        if (!response.ok) {
          throw new Error('Failed to get Firebase Custom Token from backend.');
        }

        const data = await response.json();
        const customToken = data.customToken;

        // Firebase Custom TokenでFirebaseにサインイン
        const userCredential = await signInWithCustomToken(auth, customToken);
        setFirebaseUser(userCredential.user);

        // ユーザー情報をFirestoreに保存または更新
        const userRef = doc(db, 'users', userCredential.user.uid);
        await setDoc(userRef, {
          displayName: profile.displayName,
          lineUserId: profile.userId,
          // 必要に応じて他のLINEプロフィール情報も保存
        }, { merge: true }); // 既存のフィールドは上書きせずマージ

      } catch (err: any) {
        console.error('LIFF/Firebase login error:', err);
        setError(err.message || 'An unexpected error occurred during login.');
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
      setError('Firebase user not logged in.');
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

      const response = await fetch('http://localhost:8081/api/books', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(bookData),
      });

      if (!response.ok) {
        const errorData = await response.json();
        throw new Error(errorData.message || '書籍登録に失敗しました。');
      }

      const result = await response.json();
      alert(result.message || '書籍を登録しました！');

      // フォームをクリア
      setTitle('');
      setAuthor('');
      setDeadline('');
      setInsultLevel(3);

    } catch (err: any) {
      console.error('書籍登録エラー:', err);
      setError(err.message || '書籍登録中に予期せぬエラーが発生しました。');
    } finally {
      setLoading(false);
    }
  };

  if (loading) {
    return <div className="min-h-screen flex items-center justify-center bg-gray-100 text-lg font-bold">Loading...</div>;
  }

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-red-100 text-red-700 text-lg font-bold p-4">
        Error: {error}
      </div>
    );
  }

  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-gray-100 p-4">
      <h1 className="text-4xl font-bold text-gray-800 mb-6">ツンドク・キラー</h1>

      {isLoggedIn && firebaseUser ? (
        <div className="bg-white p-8 rounded-lg shadow-md w-full max-w-md">
          <p className="text-xl font-semibold mb-4 text-center">ようこそ、{lineProfile?.displayName}さん！💖</p>
          {lineProfile?.pictureUrl && (
            <img src={lineProfile.pictureUrl} alt="Profile" className="w-24 h-24 rounded-full mx-auto mb-4" />
          )}
          <p className="text-gray-700 text-sm mb-2">Firebase UID: {firebaseUser.uid}</p>
          <p className="text-gray-600 text-sm mb-6">LIFFログインとFirebase連携が完了しました。</p>

          <h2 className="text-2xl font-bold mb-4 text-center">書籍を登録する</h2>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="title" className="block text-gray-700 text-sm font-bold mb-2">タイトル:</label>
              <input
                type="text"
                id="title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="shadow appearance-none border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
                required
              />
            </div>
            <div>
              <label htmlFor="author" className="block text-gray-700 text-sm font-bold mb-2">著者:</label>
              <input
                type="text"
                id="author"
                value={author}
                onChange={(e) => setAuthor(e.target.value)}
                className="shadow appearance-none border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
                required
              />
            </div>
            <div>
              <label htmlFor="deadline" className="block text-gray-700 text-sm font-bold mb-2">読了期限:</label>
              <input
                type="date"
                id="deadline"
                value={deadline}
                onChange={(e) => setDeadline(e.target.value)}
                className="shadow appearance-none border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
                required
              />
            </div>
            <div>
              <label htmlFor="insultLevel" className="block text-gray-700 text-sm font-bold mb-2">煽りレベル:</label>
              <select
                id="insultLevel"
                value={insultLevel}
                onChange={(e) => setInsultLevel(Number(e.target.value))}
                className="shadow border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
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
              className="bg-purple-500 hover:bg-purple-600 text-white font-bold py-2 px-4 rounded-full w-full focus:outline-none focus:shadow-outline"
            >
              書籍を登録！
            </button>
          </form>
        </div>
      ) : (
        <div className="bg-white p-8 rounded-lg shadow-md text-center">
          <p className="text-xl text-gray-700 mb-4">LIFFにログインしていません。</p>
          <button
            onClick={() => liff.login()}
            className="bg-green-500 hover:bg-green-600 text-white font-bold py-2 px-4 rounded-full focus:outline-none focus:shadow-outline"
          >
            LINEでログイン
          </button>
        </div>
      )}
    </div>
  );
}

export default App;
