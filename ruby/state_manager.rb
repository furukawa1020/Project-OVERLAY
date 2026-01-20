class StateManager
  # Configuration imported from config.rb
  
  def initialize
    @votes = [] # [{type: 'ok', ts: Time.now}, ...]
    @current_state = 'UNKNOWN'
    @split_degree = 0.0
    @strength = 0.0
  end

  def initialize
    @current_state = 'UNKNOWN'
    @tension = 0.0 # 0.0 to 10.0 (Atmosphere Heat)
    @last_update = Time.now
  end

  def process_text(text)
    # Check for Danger Words
    hit = Config::DANGER_WORDS.any? { |w| text.include?(w) }
    
    if hit
      @tension += 3.0 # Spike tension
    else
      @tension += 0.2 # Slight activity boost
    end
    
    recalculate
  end
  
  # Deprecated: add_vote is removed

  def reset
    @votes = []
    recalculate
  end

  def get_state
    cleanup_old_votes
    # Recalculate periodically even if no new votes, to handle decay
    recalculate 
    {
      state: @current_state,
      tension: @tension,
      split_degree: (@tension / 10.0).clamp(0.0, 1.0), # Map tension to split visual
    }
  end

  private

  def cleanup_old_votes
    now = Time.now
    @votes.reject! { |v| now - v[:ts] > Config::VOTE_WINDOW_SEC }
  end

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
