import { useEffect, useRef } from 'react';

// windowオブジェクトにLineItプロパティを追加
declare global {
  interface Window {
    LineIt: any;
  }
}

const LineAddFriendButton = ({ lineId }: { lineId: string }) => {
  const buttonRef = useRef(null);

  useEffect(() => {
    if (!lineId) {
      console.error('LINE IDが設定されてないよ！🥺');
      return;
    }

    // 既存のLINEスクリプトが読み込まれていたら何もしない
    if (document.getElementById('line-jssdk')) {
      return;
    }

    // LINEの外部スクリプトを動的に読み込む
    const script = document.createElement('script');
    script.src = "https://www.line-website.com/social-plugins/js/thirdparty/loader.min.js";
    script.async = true;
    script.defer = true;
    script.id = 'line-jssdk'; // 重複読み込み防止用ID
    document.body.appendChild(script);

    // スクリプトが読み込まれたら、LINEのボタンを初期化する関数を呼ぶ
    script.onload = () => {
      if (window.LineIt && window.LineIt.loadButton) {
        window.LineIt.loadButton();
      }
    };

    // コンポーネントがアンマウントされたらスクリプトをクリーンアップ（任意）
    return () => {
      // 必要ならスクリプトを削除するけど、通常は残しておいても問題ないことが多いよ
      // document.body.removeChild(script);
    };
  }, [lineId]); // lineIdが変わったら再実行

  return (
    <div ref={buttonRef}>
      {/* ここにLINE Developersで生成した<a>タグを配置するよ！ */}
      {/* data-lineidにはきみのLINE IDを入れてね！ */}
      <div
        className="line-it-button"
        data-lang="ja"
        data-type="friend"
        data-lineid="@566nverw"
        data-count="true"
        data-home="true"
        data-size="small"
      ></div>
    </div>
  );
};

export default LineAddFriendButton;
