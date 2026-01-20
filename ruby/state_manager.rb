class StateManager
  # Configuration
  WINDOW_SEC = 10
  THRESHOLD_NO = 0.2
  THRESHOLD_SPLIT = 0.3
  
  def initialize
    @votes = [] # [{type: 'ok', ts: Time.now}, ...]
    @current_state = 'UNKNOWN'
    @split_degree = 0.0
    @strength = 0.0
  end

  def add_vote(type)
    return unless ['ok', 'unsure', 'no'].include?(type)
    @votes << { type: type, ts: Time.now }
    cleanup_old_votes
    recalculate
  end

  def get_state
    cleanup_old_votes
    # Recalculate periodically even if no new votes, to handle decay
    recalculate 
    {
      state: @current_state,
      split_degree: @split_degree,
      strength: @strength,
      vote_count: @votes.size
    }
  end

  private

  def cleanup_old_votes
    now = Time.now
    @votes.reject! { |v| now - v[:ts] > WINDOW_SEC }
  end

  def recalculate
    if @votes.empty?
      @current_state = 'UNKNOWN'
      @split_degree = 0.0
      @strength = 0.0
      return
    end

    total = @votes.size.to_f
    counts = @votes.group_by { |v| v[:type] }.transform_values(&:size)
    
    ok_count = counts['ok'] || 0
    no_count = counts['no'] || 0
    unsure_count = counts['unsure'] || 0

    ok_ratio = ok_count / total
    no_ratio = no_count / total
    unsure_ratio = unsure_count / total
    
    # Split measures disagreement (NO + UNSURE)
    # Or strictly: Variance? For now usage simple ratio logic
    disagreement = no_ratio + (unsure_ratio * 0.5)

    @strength = [total / 5.0, 1.0].min # Cap strength at 5 votes for now

    if disagreement > THRESHOLD_SPLIT
      @current_state = 'SPLIT'
      @split_degree = [disagreement * 1.5, 1.0].min # Amplify split visual
    elsif total > 0
      @current_state = 'ALIGNED'
      @split_degree = 0.0
    else
      @current_state = 'UNKNOWN'
      @split_degree = 0.0
    end
  end
end
