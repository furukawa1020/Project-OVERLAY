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
    # Default Config
    config = { style: 'normal', rot: 0.0, scalex: 1.0, color: 'white', vy_mult: 1.0 }

    # Inversion Logic
    if (text.include?("上下") || text.include?("天井") || text.include?("逆さま")) && (text.include?("反転") || text.include?("逆"))
      config[:style] = 'invert_v'
      config[:rot] = 3.14159
      config[:vy_mult] = -1.0
      config[:color] = 'cyan'
      return config
    end

    if (text.include?("左右") || text.include?("鏡")) && (text.include?("反転") || text.include?("逆"))
      config[:style] = 'invert_h'
      config[:scalex] = -1.0
      config[:color] = 'cyan'
      return config
    end

    if (text.include?("色") || text.include?("カラー")) && (text.include?("反転") || text.include?("違う"))
      config[:style] = 'invert_c'
      config[:color] = 'cyan'
      return config
    end
    
    # Impact Logic (Shaft Cut-ins)
    if ["絶対", "嘘", "違う", "矛盾", "変", "おかしい"].any? { |w| text.include?(w) }
      config[:style] = 'impact' # Special style for Go to handle
      config[:flash] = true
      config[:shake] = 20.0 # High instant shake
      config[:color] = 'red'
      config[:scale] = 2.5
      return config
    end

    if ["でも", "しかし", "だが", "逆に", "とは言え", "けど", "反対に"].any? { |w| text.include?(w) }
      config[:style] = 'conjunction'
      config[:scalex] = -1.0
      config[:rot] = 3.14159
      config[:color] = 'yellow'
      return config
    end

    if ["えっと", "うーん", "あの", "多分", "かな", "なんか", "えー"].any? { |w| text.include?(w) }
      config[:style] = 'hesitation'
      config[:color] = 'grey'
      return config
    end

    config
  end
  
  def check_silence
    # Returns word and CONFIG Hash
    now = Time.now
    duration = now - @last_speech_time
    
    next_word = nil
    config = nil

    if duration > 2.0 && @silence_stage == 0
      next_word = "..."
      config = { style: 'silence_dots', color: 'grey_alpha', scale: 0.8 }
      @silence_stage = 1
    elsif duration > 5.0 && @silence_stage == 1
      next_word = "間"
      config = { style: 'silence_ma', color: 'blue_white', vy: 0.0, scale: 1.0 }
      @silence_stage = 2
    elsif duration > 8.0 && @silence_stage == 2
      next_word = "沈黙"
      config = { style: 'silence_heavy', color: 'dark_grey', vy: 15.0, scale: 1.5 }
      @silence_stage = 3
    elsif duration > 12.0 && @silence_stage == 3
      next_word = "静寂"
      config = { style: 'silence_abyss', color: 'black', vy: -1.0, scale: 2.0 }
      @silence_stage = 4
    elsif duration > 17.0 && @silence_stage == 4
      # Loop / Keep spawning Abyss words periodically
      next_word = "..." 
      config = { style: 'silence_dots', color: 'grey_alpha', scale: 1.0 }
      @last_speech_time = Time.now - 2.0 # Reset slightly to loop back to dots? No, let's just spawn and keep stage 4
      @last_speech_time = Time.now - 12.0 # Keeps it at stage 4 threshold
    end
    
    return next_word, config
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
