# OVERLAY

conversation_state != content_analysis
ruby = meaning (意味・状態)
go = spatial (空間・描画)

## 概要
会話の齟齬・非対称性を、空間の状態として露出させるツール。
言葉そのものではなく、会話の「状態（揃っているか、割れているか）」を可視化します。

## アーキテクチャ (言語分業)

*   **Ruby Layer**: 意味・状態・合意構造 (State Engine)
    *   Sinatra + faye-websocket
*   **Go Layer**: 空間・描画・即時反応 (Spatial Renderer)
    *   Ebitengine (v2)

## ディレクトリ構成
*   `ruby/`: サーバーサイド（状態管理、管理UI、参加者UI）
*   `go/`: クライアントサイド（空間描画、フルスクリーン表示）
