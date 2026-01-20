module Config
  # 危険ワードリスト（参加者UIに表示される）
  DANGER_WORDS = [
    "一旦",
    "普通",
    "簡単",
    "ちゃんと",
    "なる早",
    "みんな",
    "常識",
    "直感"
  ]

  # 状態判定の閾値
  THRESHOLD_NO = 0.2
  THRESHOLD_SPLIT = 0.3
  
  # 投票の有効期間(秒)
  VOTE_WINDOW_SEC = 10
end
