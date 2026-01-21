class StateManager
  # Configuration imported from config.rb
  
  def initialize
    @current_state = 'UNKNOWN'
    @tension = 0.0 # 0.0 to 10.0 (Atmosphere Heat)
    @last_update = Time.now
    @last_speech_time = Time.now
    @silence_stage = 0
  end

  def process_text(text)
    @last_speech_time = Time.now
    @silence_stage = 0
    
    # Check for Danger Words
    hit = Config::DANGER_WORDS.any? { |w| text.include?(w) }
    
    if hit
      @tension += 3.0 # Spike tension
    else
      @tension += 0.2 # Slight activity boost
    end
    
    recalculate
    
    analyze_semantics(text)
  end
  
  def analyze_semantics(text)
    # Ruby-strength: Flexible text analysis
    return :conjunction if ["でも", "しかし", "だが", "逆に", "とは言え", "けど", "反対に"].any? { |w| text.include?(w) }
    return :hesitation if ["えっと", "うーん", "あの", "多分", "かな", "なんか", "えー"].any? { |w| text.include?(w) }
    return :question if text.include?("？") || text.include?("?")
    :normal
  end
  
  def check_silence
    # Returns a silence word if a threshold is crossed, else nil
    now = Time.now
    duration = now - @last_speech_time
    
    next_word = nil
    style = nil

    if duration > 2.0 && @silence_stage == 0
      next_word = "..."
      style = :silence_dots
      @silence_stage = 1
    elsif duration > 5.0 && @silence_stage == 1
      next_word = "間"
      style = :silence_ma
      @silence_stage = 2
    elsif duration > 8.0 && @silence_stage == 2
      next_word = "沈黙"
      style = :silence_heavy
      @silence_stage = 3
    elsif duration > 12.0 && @silence_stage == 3
      next_word = "静寂"
      style = :silence_abyss
      @silence_stage = 4
    end
    
    return next_word, style
  end
  
  def reset
    @tension = 0.0
    @current_state = 'UNKNOWN'
    @last_speech_time = Time.now
    @silence_stage = 0
    recalculate
  end

  def get_state
    # Recalculate periodically even if no new votes, to handle decay
    recalculate 
    {
      state: @current_state,
      tension: @tension,
      split_degree: (@tension / 10.0).clamp(0.0, 1.0), # Map tension to split visual
    }
  end

  private

  def recalculate
    now = Time.now
    dt = now - @last_update
    @last_update = now

    # Natural Decay (Cooling down)
    @tension -= dt * 0.5
    @tension = 0.0 if @tension < 0

    # Tension Thresholds
    if @tension > 8.0
      @current_state = 'SPLIT' # Chaos/Conflict
    elsif @tension > 2.0
      @current_state = 'ALIGNED' # Active Conversation
    else
      @current_state = 'UNKNOWN' # Silence
    end
  end
end
